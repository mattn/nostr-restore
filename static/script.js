function copyEventData(button) {
    // Find the parent event div and then the event-content div
    const eventDiv = button.closest('.event');
    const contentDiv = eventDiv.querySelector('.event-content');
    const content = contentDiv.getAttribute('data-content');
    
    navigator.clipboard.writeText(content).then(function() {
        // Change button text temporarily to indicate success
        const originalText = button.textContent;
        button.textContent = 'Copied!';
        
        setTimeout(() => {
            button.textContent = originalText;
        }, 2000);
    }).catch(function(err) {
        console.error('Failed to copy: ', err);
        alert('Failed to copy to clipboard');
    });
}

function showRestoreConfirmation(button) {
    // Find the parent event div and then the event-content div
    const eventDiv = button.closest('.event');
    const contentDiv = eventDiv.querySelector('.event-content');
    const eventData = contentDiv.getAttribute('data-content');
    
    // Parse the event data
    const event = JSON.parse(eventData);
    
    // Define the relays to send to
    const relays = [
        "wss://relay.damus.io",
        "wss://nostr-pub.wellorder.net", 
        "wss://relay.nostr.band",
        "wss://nostr-relay.nokotaro.com",
        "wss://nostr.bitcoiner.social"
    ];
    
    // Show confirmation dialog
    const relayList = relays.join('\n');
    const confirmed = confirm(`Restore this event to the following relays?\n\n${relayList}`);
    
    if (confirmed) {
        restoreEvent(event, relays);
    }
}

async function restoreEvent(event, relays) {
    try {
        // Update the timestamp to now
        const updatedEvent = {
            ...event,
            created_at: Math.floor(Date.now() / 1000),
        };
        
        // Request signature via NIP-07
        if (!window.nostr) {
            alert('Nostr extension not found. Please install a Nostr extension like Alby, nos2x or Flue.');
            return;
        }
        
        const signedEvent = await window.nostr.signEvent(updatedEvent);
        
        // Send to relays
        relays.forEach(relayUrl => {
            sendEventToRelay(signedEvent, relayUrl);
        });
        
        alert('Event restoration initiated. Check your Nostr client for status.');
    } catch (error) {
        console.error('Error during restoration:', error);
        alert('Error during restoration: ' + error.message);
    }
}

function sendEventToRelay(event, relayUrl) {
    try {
        const ws = new WebSocket(relayUrl);
        
        ws.onopen = () => {
            const request = ["EVENT", event];
            ws.send(JSON.stringify(request));
        };
        
        ws.onclose = () => {
            console.log(`Disconnected from ${relayUrl}`);
        };
        
        ws.onerror = (error) => {
            console.error(`Error connecting to ${relayUrl}:`, error);
        };
    } catch (error) {
        console.error(`Failed to connect to ${relayUrl}:`, error);
    }
}