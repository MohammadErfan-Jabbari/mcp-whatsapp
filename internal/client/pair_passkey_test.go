package client

import (
	"errors"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
)

// WhatsApp can ask a linking device to answer a passkey challenge, and this
// server has no authenticator to answer with. Such an event ends the
// attempt with an error that says why; events that belong to a normal
// pairing do not.
func TestPairingEventError(t *testing.T) {
	cause := errors.New("failed to parse passkey notification")
	for _, tc := range []struct {
		name string
		item whatsmeow.QRChannelItem
		want error
	}{
		{"passkey request", whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventPasskeyRequest, PasskeyRequest: &events.PairPasskeyRequest{}}, ErrPasskeyRequired},
		{"pairing error", whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventError, Error: cause}, cause},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := PairingEventError(tc.item); !errors.Is(err, tc.want) {
				t.Fatalf("PairingEventError(%s) = %v, want %v", tc.item.Event, err, tc.want)
			}
		})
	}
	for _, item := range []whatsmeow.QRChannelItem{
		{Event: whatsmeow.QRChannelEventCode, Code: "2@abc"},
		whatsmeow.QRChannelSuccess,
		whatsmeow.QRChannelTimeout,
		{},
	} {
		if err := PairingEventError(item); err != nil {
			t.Errorf("PairingEventError(%q) = %v, want nil", item.Event, err)
		}
	}
	if msg := ErrPasskeyRequired.Error(); !strings.Contains(msg, "passkey") || !strings.Contains(msg, "cannot") {
		t.Errorf("ErrPasskeyRequired = %q, want it to say a passkey was asked for and cannot be given", msg)
	}
}
