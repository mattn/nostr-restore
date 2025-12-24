package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

//go:embed static/*
var staticFiles embed.FS

// Event represents a nostr event from event_backup
type Event struct {
	ID        string
	Pubkey    string
	CreatedAt int64
	Kind      int
	EventData string // JSON data containing the full event
}

// UserProfile holds user profile information from kind 0 events
type UserProfile struct {
	Name    string `json:"name"`
	About   string `json:"about"`
	Picture string `json:"picture"`
	Nip05   string `json:"nip05"`
}

// GetFormattedDate returns the created_at timestamp as a human-readable date
func (e Event) GetFormattedDate() string {
	return time.Unix(e.CreatedAt, 0).Format("2006-01-02 15:04:05")
}

// fetchProfileFromRelays attempts to fetch user profile (kind 0) from relays
func fetchProfileFromRelays(pubkey string) (*UserProfile, error) {
	// Create a filter to get kind 0 event for the pubkey
	filter := nostr.Filter{
		Authors: []string{pubkey},
		Kinds:   []int{0},
		Limit:   1,
	}

	// Try common public relays
	relays := []string{
		"wss://relay.damus.io",
		"wss://yabu.me",
		"wss://nostr.compile-error.net",
	}

	ctx := context.Background()
	for _, relayURL := range relays {
		relay, err := nostr.RelayConnect(ctx, relayURL)
		if err != nil {
			log.Printf("Failed to connect to relay %s: %v", relayURL, err)
			continue
		}

		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		evs, err := relay.Subscribe(ctx, []nostr.Filter{filter})
		if err != nil {
			cancel()
			relay.Close()
			log.Printf("Failed to subscribe to relay %s: %v", relayURL, err)
			continue
		}

		for ev := range evs.Events {
			if ev.Kind == 0 {
				var profile UserProfile
				err := json.Unmarshal([]byte(ev.Content), &profile)
				if err != nil {
					log.Printf("Failed to unmarshal profile from event: %v", err)
					continue
				}
				cancel()
				relay.Close()
				return &profile, nil
			}
		}
		cancel()
		relay.Close()
	}

	// If no profile found, return empty profile
	return &UserProfile{}, nil
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/npub/", npubHandler(db))

	// Serve embedded static files
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}
	staticServer := http.FileServer(http.FS(staticFS))
	http.Handle("/static/", http.StripPrefix("/static/", staticServer))

	log.Printf("Server starting on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// homeHandler serves the static homepage with service introduction
func homeHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	tmpl := `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Nostr Event Restore Service</title>
    <link rel="stylesheet" href="/static/style.css">
    <script src="https://cdn.jsdelivr.net/npm/sweetalert2@11"></script>
    <script src="/static/script.js"></script>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>Nostr Event Restore Service</h1>
            <p>This service allows you to restore and view Nostr events by npub identifier.</p>
            <p>Enter an npub (e.g., npub1...) in the box below to view stored events.</p>
        </div>

        <div class="search-box">
            <form action="/npub/" method="GET">
                <input type="text" name="q" placeholder="Enter npub (e.g., npub1...)" />
                <button type="submit">Search Events</button>
            </form>
        </div>

        <footer>
            <p>Nostr Event Restore Service &copy; 2025</p>
        </footer>
    </div>
</body>
</html>
`
	tmpl = strings.TrimSpace(tmpl)
	t, err := template.New("home").Parse(tmpl)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = t.Execute(w, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// npubHandler handles npub lookup and event display
func npubHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		npub := strings.TrimPrefix(r.URL.Path, "/npub/")

		// If npub not in URL path, check query param
		if npub == "" {
			npub = r.URL.Query().Get("q")
		}

		// Validate and convert npub to hex
		hexPubkey, err := npubToHex(npub)
		if err != nil {
			http.Error(w, "Invalid npub format", http.StatusBadRequest)
			return
		}

		// Query events by pubkey from event_backup table
		events, err := queryEventsByPubkey(db, hexPubkey)
		if err != nil {
			http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
			return
		}

		// Fetch user profile from relays
		profile, err := fetchProfileFromRelays(hexPubkey)
		if err != nil {
			log.Printf("Error fetching profile for %s: %v", hexPubkey, err)
			profile = &UserProfile{} // Use empty profile if fetch fails
		}

		// Render events template
		tmpl := `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Events for {{.Profile.Name}}</title>
    <link rel="stylesheet" href="/static/style.css">
    <script src="https://cdn.jsdelivr.net/npm/sweetalert2@11"></script>
    <script src="/static/script.js"></script>
</head>
<body>
    <div class="container">
        <div class="back-link">
            <a href="/">← Back to Home</a>
        </div>

        <div class="profile-header" style="display: flex; align-items: center; margin-bottom: 30px; padding-bottom: 20px; border-bottom: 1px solid #eee;">
            {{if .Profile.Picture}}
            <img src="{{.Profile.Picture}}" alt="Profile Picture" class="profile-pic" style="width: 60px; height: 60px; border-radius: 50%; object-fit: cover; margin-right: 15px;">
            {{end}}
            <div>
                <h1>{{if .Profile.Name}}{{.Profile.Name}}{{else}}Nostr User{{end}}</h1>
                <p><strong>npub:</strong> {{.Npub}}</p>
                <p><strong>Hex Pubkey:</strong> {{.HexPubkey}}</p>
                {{if .Profile.Nip05}}<p><strong>Verification:</strong> {{.Profile.Nip05}}</p>{{end}}
                {{if .Profile.About}}<p><strong>About:</strong> {{.Profile.About}}</p>{{end}}
                <p><strong>Total Events Found:</strong> {{len .Events}}</p>
            </div>
        </div>

        <div class="events-container">
            {{$currentKind := -1}}
            {{range .Events}}
                {{if ne .Kind $currentKind}}
                    {{if ne $currentKind -1}}</div>{{end}}
                    <div class="kind-group">
                        <h2 class="kind-header">Kind {{.Kind}}</h2>
                    {{$currentKind = .Kind}}
                {{end}}
                <div class="event">
                    <div class="event-header">
                        <div class="event-header-left">
                            <span class="event-timestamp">{{.GetFormattedDate}}</span>
                        </div>
                        <div class="event-actions">
                            {{if eq .Kind 3}}<button class="restore-btn" onclick="showRestoreConfirmation(this)">Restore</button>{{end}}
                            <button class="copy-btn" onclick="copyEventData(this)">Copy</button>
                        </div>
                    </div>
                    <detail>
                        <div class="event-content" data-content="{{.EventData}}"><pre style="white-space: pre-wrap; word-break: break-all;">{{.EventData}}</pre></div>
                    </detail>
                    <div class="event-id">{{.ID}}</div>
                </div>
            {{else}}
                <p>No events found for this pubkey.</p>
            {{end}}
            {{if gt (len .Events) 0}}</div>{{end}}
        </div>
        <footer>
            <p>Nostr Event Restore Service &copy; 2025</p>
        </footer>
    </div>
</body>
</html>
`
		t, err := template.New("events").Parse(tmpl)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data := struct {
			Npub      string
			HexPubkey string
			Events    []Event
			Profile   *UserProfile
		}{
			Npub:      npub,
			HexPubkey: hexPubkey,
			Events:    events,
			Profile:   profile,
		}

		err = t.Execute(w, data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

// npubToHex converts npub string to hex pubkey
func npubToHex(npub string) (string, error) {
	if !strings.HasPrefix(npub, "npub1") {
		return "", fmt.Errorf("invalid npub format: does not start with npub1")
	}

	// Decode the npub using nip19
	prefix, value, err := nip19.Decode(npub)
	if err != nil {
		return "", fmt.Errorf("invalid npub: %v", err)
	}

	if prefix != "npub" {
		return "", fmt.Errorf("not an npub: prefix is %s", prefix)
	}

	// Convert the decoded value to hex string
	hexPubkey, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("decoded npub value is not a string")
	}

	return hexPubkey, nil
}

// queryEventsByPubkey retrieves events from event_backup table by pubkey
func queryEventsByPubkey(db *sql.DB, pubkey string) ([]Event, error) {
	// Sort by event_kind ASC (0 to higher), then by created_at DESC (newest first)
	query := `SELECT id, pubkey, created_at, event_kind, event_data FROM event_backup WHERE pubkey = $1 ORDER BY event_kind ASC, created_at DESC`
	rows, err := db.Query(query, pubkey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var event Event
		err := rows.Scan(&event.ID, &event.Pubkey, &event.CreatedAt, &event.Kind, &event.EventData)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}

	return events, nil
}
