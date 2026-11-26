package updates

import (
	"fmt"
	"strings"
	"time"
)

// Summary is the one line an admin reads before confirming: who notices
// this update, and for how long. comps are the components being applied.
func Summary(comps []Component) string {
	if len(comps) == 0 {
		return "Nothing selected."
	}
	var chat, calls, uploads, web bool
	var chatDown, callsDown time.Duration
	var quiet []string
	self, webApp := false, false
	for _, c := range comps {
		im := c.Impact
		switch {
		case im.ChatDisconnect:
			chat = true
			chatDown = max(chatDown, im.Downtime)
		case c.ID == "webircgateway":
			web = true
			chatDown = max(chatDown, im.Downtime)
		case im.DropsCalls:
			calls = true
			callsDown = max(callsDown, im.Downtime)
		case c.ID == "files":
			uploads = true
		case c.ID == "web":
			webApp = true
		case c.Self:
			self = true
		default:
			quiet = append(quiet, c.Name)
		}
	}
	var clauses []string
	switch {
	case chat:
		clauses = append(clauses, fmt.Sprintf("disconnect all chat users for ~%s (clients reconnect automatically)", secs(chatDown)))
	case web:
		clauses = append(clauses, fmt.Sprintf("disconnect web client users from chat for ~%s (they reconnect automatically)", secs(chatDown)))
	}
	if calls {
		clauses = append(clauses, fmt.Sprintf("drop ongoing voice and video calls for ~%s", secs(callsDown)))
	}
	if uploads {
		clauses = append(clauses, "make uploads in progress fail")
	}
	s := "Nobody is disconnected."
	if len(clauses) > 0 {
		s = "This update will " + joinAnd(clauses) + "."
	}
	if len(quiet) > 0 {
		verb := " pause"
		if len(quiet) == 1 {
			verb = " pauses"
		}
		s += " " + joinAnd(quiet) + verb + " for a few seconds."
	}
	if webApp {
		s += " Web client users get the new version the next time they load it."
	}
	if self {
		s += " The panel reloads at the end."
	}
	return s
}

func secs(d time.Duration) string {
	if d <= 0 {
		d = 5 * time.Second
	}
	return fmt.Sprintf("%d seconds", int(d.Seconds()))
}

func joinAnd(p []string) string {
	switch len(p) {
	case 0:
		return ""
	case 1:
		return p[0]
	}
	return strings.Join(p[:len(p)-1], ", ") + " and " + p[len(p)-1]
}

// NonDisruptive keeps the components that don't disconnect anyone or break
// what they're doing ("apply without disconnecting users").
func NonDisruptive(comps []Component) (keep, skipped []Component) {
	for _, c := range comps {
		if c.Impact.Disruptive {
			skipped = append(skipped, c)
		} else {
			keep = append(keep, c)
		}
	}
	return keep, skipped
}
