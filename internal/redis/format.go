package redis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// FormatTTL is a TTL as the two largest units, so `3d 4h` rather than seconds
// nobody can read at a glance. -1 is a key that never expires, and any other
// negative one that is gone.
func FormatTTL(seconds int64) string {
	if seconds == -1 {
		return "No expiry"
	}
	if seconds < 0 {
		return "Expired"
	}
	units := []struct {
		label string
		size  int64
	}{{"d", 86400}, {"h", 3600}, {"m", 60}, {"s", 1}}
	var parts []string
	rest := seconds
	for _, u := range units {
		n := rest / u.size
		if n > 0 || (u.label == "s" && len(parts) == 0) {
			parts = append(parts, fmt.Sprintf("%d%s", n, u.label))
		}
		rest -= n * u.size
		if len(parts) == 2 {
			break
		}
	}
	return strings.Join(parts, " ")
}

// FormatReply lays a reply out the way redis-cli prints it, so output reads
// as in the terminal people know: `(nil)`, `(integer) 3`, numbered and
// indented array items. A binary bulk string shows its escaped label.
func FormatReply(r Reply) string { return formatReply(r, "") }

func formatReply(r Reply, indent string) string {
	switch r.Kind {
	case ReplyNil:
		return "(nil)"
	case ReplyInt:
		return "(integer) " + strconv.FormatInt(r.Int, 10)
	case ReplyBytes:
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		enc.Encode(r.Bytes.Text)
		return strings.TrimSuffix(b.String(), "\n")
	case ReplyError:
		return "(error) " + r.Err
	}
	if len(r.Items) == 0 {
		return "(empty array)"
	}
	width := len(strconv.Itoa(len(r.Items)))
	var out []string
	for i, item := range r.Items {
		label := fmt.Sprintf("%*d) ", width, i+1)
		nested := formatReply(item, indent+strings.Repeat(" ", len(label)))
		if i == 0 {
			out = append(out, label+nested)
		} else {
			out = append(out, indent+label+nested)
		}
	}
	return strings.Join(out, "\n")
}
