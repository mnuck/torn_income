package env

import (
	"bufio"
	"os"
	"strings"
)

func unquote(v string) string {
	if len(v) < 2 {
		return v
	}
	if v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
		var b strings.Builder
		escaped := false
		for i := 0; i < len(v); i++ {
			c := v[i]
			if escaped {
				switch c {
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				case 't':
					b.WriteByte('\t')
				case '\\':
					b.WriteByte('\\')
				case '"':
					b.WriteByte('"')
				default:
					b.WriteByte('\\')
					b.WriteByte(c)
				}
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			b.WriteByte(c)
		}
		if escaped {
			b.WriteByte('\\')
		}
		return b.String()
	}
	if v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1]
	}
	return v
}

// Load reads a file of KEY=VALUE lines (ignoring blanks and comments)
// and puts them into the process environment.
func Load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		val = unquote(val)
		os.Setenv(key, val)
	}
	return sc.Err()
}
