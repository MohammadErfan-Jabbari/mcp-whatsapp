package client

import (
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow"
)

// ErrPasskeyRequired reports that WhatsApp asked this device to answer a
// passkey (WebAuthn) challenge before it would finish linking. This server
// has no authenticator to answer with, so the pairing attempt cannot
// complete.
var ErrPasskeyRequired = errors.New("WhatsApp asked for a passkey to finish linking this device, and this server cannot answer passkey requests; pairing stopped")

// PairingEventError returns the error that ends a pairing attempt at a QR
// channel item, or nil when the attempt should keep waiting. whatsmeow keeps
// the channel open after a passkey request and after a passkey error, so
// without this check a caller waits until the QR codes run out.
func PairingEventError(item whatsmeow.QRChannelItem) error {
	switch item.Event {
	case whatsmeow.QRChannelEventPasskeyRequest:
		return ErrPasskeyRequired
	case whatsmeow.QRChannelEventError:
		if item.Error == nil {
			return errors.New("pairing failed")
		}
		return fmt.Errorf("pairing failed: %w", item.Error)
	}
	return nil
}
