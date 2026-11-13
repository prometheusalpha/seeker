package driver

import (
	"slices"
	"strings"

	"seeker/internal/model"
)

// ReadURLParam reads one parameter out of a raw query string, the way the
// driver itself parses it: a leading "?" or "&" is tolerated, "+" is a
// space, and text that does not decode is kept as it was typed. It returns
// the first match, as recall's dialog does; the drivers keep the last.
func ReadURLParam(raw, key string) string {
	for _, part := range strings.Split(strings.TrimLeft(strings.TrimSpace(raw), "?&"), "&") {
		if part == "" {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		if strings.TrimSpace(name) != key {
			continue
		}
		return decodeParam(strings.TrimSpace(value))
	}
	return ""
}

// SetURLParam rewrites one parameter. Every name in keys stands for the
// same setting, so all of them go first; an empty value removes it. Values
// are written as they are, not re-encoded, as recall's dialog writes them.
func SetURLParam(raw string, keys []string, value string) string {
	var kept []string
	for _, part := range strings.Split(strings.TrimLeft(strings.TrimSpace(raw), "?&"), "&") {
		if part == "" {
			continue
		}
		name, _, _ := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		if slices.Contains(keys, name) {
			continue
		}
		kept = append(kept, strings.TrimSpace(part))
	}
	if value != "" {
		kept = append(kept, keys[0]+"="+value)
	}
	return strings.Join(kept, "&")
}

// tlsModeKeys are the names of the one setting a TLS mode carries.
func tlsModeKeys(t model.DBType) []string {
	if t == model.MySQL {
		return []string{"ssl-mode", "sslmode", "ssl"}
	}
	return []string{"sslmode"}
}

// TLSModes lists the modes a profile can choose, in the order the dialog
// shows them.
func TLSModes(t model.DBType) []string {
	if t == model.MySQL {
		return []string{"disabled", "preferred", "required", "verify_ca", "verify_identity"}
	}
	return []string{"disable", "prefer", "require", "verify-ca", "verify-full"}
}

// TLSModeLabels is the mode names as the dialog spells them out.
func TLSModeLabels(t model.DBType) []string {
	if t == model.MySQL {
		return []string{"Disabled", "Preferred", "Required", "Verify CA", "Verify Identity"}
	}
	return []string{"Disable", "Prefer", "Require", "Verify CA", "Verify Full"}
}

// tlsParam is what a mode writes into the URL parameters, and whether it
// turns TLS on. MySQL's two plain modes are the bare on/off flag: they need
// no parameter, and "preferred" is still on.
func tlsParam(t model.DBType, mode string) (string, bool) {
	if t == model.MySQL {
		switch mode {
		case "disabled":
			return "", false
		case "preferred":
			return "", true
		case "required":
			return "require", true
		case "verify_ca":
			return "verify-ca", true
		case "verify_identity":
			return "verify-full", true
		}
		return "", false
	}
	switch mode {
	case "disable":
		return "", false
	case "prefer", "require", "verify-ca", "verify-full":
		return mode, true
	}
	return "", false
}

// TLSMode is the mode a profile asks for: its parameter when it has one,
// else its ssl switch.
func TLSMode(t model.DBType, ssl bool, params string) string {
	for _, key := range tlsModeKeys(t) {
		raw := ReadURLParam(params, key)
		if raw == "" {
			continue
		}
		if t == model.Postgres {
			for _, m := range TLSModes(t) {
				if m == raw {
					return m
				}
			}
			return "disable"
		}
		switch raw {
		case "verify-ca":
			return "verify_ca"
		case "verify-full":
			return "verify_identity"
		case "require", "required", "true", "1":
			return "required"
		}
		return "disabled"
	}
	if ssl {
		if t == model.MySQL {
			return "preferred"
		}
		return "prefer"
	}
	if t == model.MySQL {
		return "disabled"
	}
	return "disable"
}

// WithTLSMode is the profile with a mode applied: the parameter rewritten
// and the ssl switch kept in step, so the two never disagree.
func WithTLSMode(p model.Profile, mode string) model.Profile {
	value, on := tlsParam(p.DBType, mode)
	p.URLParams = SetURLParam(p.URLParams, tlsModeKeys(p.DBType), value)
	p.SSL = on
	return p
}
