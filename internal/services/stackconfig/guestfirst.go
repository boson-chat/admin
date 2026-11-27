package stackconfig

import (
	"fmt"
	"strings"
)

// Guest-first web entry (auth.guest_first): Boson web clients open straight
// into chat as a guest and land on auth.guest_first_channel ("" or unset =
// the first auto-join network channel guests may join).
const (
	KeyGuestAccess       = "auth.guest_access"
	KeyGuestFirst        = "auth.guest_first"
	KeyGuestFirstChannel = "auth.guest_first_channel"
)

// optionalChannel checks auth.guest_first_channel: "" or a channel name.
func optionalChannel(s string) (string, error) {
	if s = strings.TrimSpace(s); s == "" {
		return "", nil
	}
	return ChannelName(s)
}

// CheckGuestFirst is auth's rule for the guest-first settings: guest_first
// needs guest_access, and the landing channel can't be a network channel
// only some roles may join.
func CheckGuestFirst(guestAccess, guestFirst bool, channel string, chans []NetworkChannel) error {
	if guestFirst && !guestAccess {
		return fmt.Errorf("guests first needs guest access: turn guest access on, or guests first off")
	}
	if channel = strings.TrimSpace(channel); channel != "" {
		for _, c := range chans {
			if FoldChannel(c.Name) == FoldChannel(channel) && c.Restricted() {
				return fmt.Errorf("%s is kept to some roles, so guests can't land there", c.Name)
			}
		}
	}
	return nil
}
