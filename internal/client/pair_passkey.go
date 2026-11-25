package client

import (
	"errors"

	"go.mau.fi/whatsmeow"
)

// ErrPasskeyRequired is a stub; the next commit defines it.
var ErrPasskeyRequired = errors.New("passkey")

// PairingEventError is a stub; the next commit defines it.
func PairingEventError(item whatsmeow.QRChannelItem) error {
	return nil
}
