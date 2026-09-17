package awsm

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// BlockedReason is what the refresh daemon cannot do on its own.
//
// These values are written by awsm's daemon into its state file. They are read
// rather than recomputed so the panel says exactly what the notification said.
type BlockedReason string

const (
	// BlockedMFA means a code has to be typed, which needs a terminal.
	BlockedMFA BlockedReason = "mfa"
	// BlockedSSO means the session needs a browser login.
	BlockedSSO BlockedReason = "sso"
	// BlockedOther covers a profile awsm cannot renew at all.
	BlockedOther BlockedReason = "other"
)

// Short renders the reason for the status bar, where every character costs.
func (b BlockedReason) Short() string {
	switch b {
	case BlockedMFA:
		return "MFA"
	case BlockedSSO:
		return "SSO"
	case BlockedOther:
		return "!"
	default:
		return ""
	}
}

// DaemonState is the part of awsm's daemon state file this panel reads.
//
// The panel never renews credentials on a timer: the daemon owns that, and two
// writers racing on ~/.aws/credentials would be a bug nobody could reproduce.
// What the panel does is show what the daemon already decided.
type DaemonState struct {
	LastRun        string        `json:"last_run"`
	LastAction     string        `json:"last_action"`
	LastReason     string        `json:"last_reason"`
	LastError      string        `json:"last_error"`
	Blocked        BlockedReason `json:"blocked"`
	BlockedProfile string        `json:"blocked_profile"`
}

// BlockedFor reports what is blocking the given profile, if anything.
//
// A warning recorded for one profile must not appear next to another: the
// daemon follows the active profile, and a stale entry would otherwise outlive
// the situation that produced it.
func (s DaemonState) BlockedFor(profile string) (BlockedReason, bool) {
	if s.Blocked == "" || profile == "" || s.BlockedProfile != profile {
		return "", false
	}
	return s.Blocked, true
}

// ReadDaemonState loads the daemon's state file.
//
// A missing or unreadable file is not an error: the daemon is optional, and a
// panel that refused to open because a file it does not own is malformed would
// be worse than one that shows no warning.
func ReadDaemonState() DaemonState {
	home, err := os.UserHomeDir()
	if err != nil {
		return DaemonState{}
	}
	data, err := os.ReadFile(filepath.Join(home, ".awsm", "daemon-state.json"))
	if err != nil {
		return DaemonState{}
	}
	var state DaemonState
	if err := json.Unmarshal(data, &state); err != nil {
		return DaemonState{}
	}
	return state
}
