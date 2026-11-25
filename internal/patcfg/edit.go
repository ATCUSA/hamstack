package patcfg

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
)

// Reading and changing named settings of a Pat config. The file also holds the Winlink password, so nothing here
// returns the file or any setting nobody asked for by name.

func readFields(path string) ([]byte, map[string]json.RawMessage, error) {
	orig, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(orig, &fields); err != nil {
		return nil, nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	return orig, fields, nil
}

// writeFields rewrites the file with sorted keys, after keeping the original once as path+".hamstack-bak".
func writeFields(path string, orig []byte, fields map[string]json.RawMessage) error {
	out, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	if bak := path + ".hamstack-bak"; !exists(bak) {
		if err := os.WriteFile(bak, orig, 0o600); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// tree reads the top-level setting parts[0] as a decoded value.
func tree(fields map[string]json.RawMessage, top string) (any, bool) {
	raw, ok := fields[top]
	if !ok {
		return nil, false
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil, false
	}
	return v, true
}

func getPath(fields map[string]json.RawMessage, parts []string) (any, bool) {
	v, ok := tree(fields, parts[0])
	if !ok {
		return nil, false
	}
	for _, p := range parts[1:] {
		m, isMap := v.(map[string]any)
		if !isMap {
			return nil, false
		}
		if v, ok = m[p]; !ok {
			return nil, false
		}
	}
	return v, true
}

// setPath puts v at the path, making the objects on the way if they are not there.
func setPath(fields map[string]json.RawMessage, parts []string, v any) error {
	if len(parts) == 1 {
		b, _ := json.Marshal(v)
		fields[parts[0]] = b
		return nil
	}
	root, _ := tree(fields, parts[0])
	if root == nil {
		root = map[string]any{}
	}
	cur, ok := root.(map[string]any)
	if !ok {
		return fmt.Errorf("%q is not an object", parts[0])
	}
	for _, p := range parts[1 : len(parts)-1] {
		next, isMap := cur[p].(map[string]any)
		if !isMap {
			if _, there := cur[p]; there && cur[p] != nil {
				return fmt.Errorf("%q is not an object", p)
			}
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = v
	b, _ := json.Marshal(root)
	fields[parts[0]] = b
	return nil
}

// deletePath removes the setting, and any object it leaves empty.
func deletePath(fields map[string]json.RawMessage, parts []string) {
	if len(parts) == 1 {
		delete(fields, parts[0])
		return
	}
	root, ok := tree(fields, parts[0])
	if !ok {
		return
	}
	var chain []map[string]any
	cur, isMap := root.(map[string]any)
	if !isMap {
		return
	}
	chain = append(chain, cur)
	for _, p := range parts[1 : len(parts)-1] {
		next, isMap := cur[p].(map[string]any)
		if !isMap {
			return
		}
		cur = next
		chain = append(chain, cur)
	}
	delete(cur, parts[len(parts)-1])
	for i := len(chain) - 1; i > 0 && len(chain[i]) == 0; i-- { // an object that held only what was removed goes too
		delete(chain[i-1], parts[i])
	}
	if len(chain[0]) == 0 {
		delete(fields, parts[0])
		return
	}
	b, _ := json.Marshal(root)
	fields[parts[0]] = b
}

// Revert puts back what Apply changed, but only the settings that still hold what Apply gave them: one the person
// has changed since is theirs now. It returns how many it put back.
func Revert(path string, changes []Change) (int, error) {
	orig, fields, err := readFields(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := len(changes) - 1; i >= 0; i-- {
		c := changes[i]
		parts := strings.Split(c.Path, ".")
		cur, ok := getPath(fields, parts)
		var want any
		if json.Unmarshal([]byte(c.New), &want) != nil || !ok || !reflect.DeepEqual(cur, want) {
			continue
		}
		if c.WasSet {
			var old any
			if json.Unmarshal([]byte(c.Old), &old) != nil {
				continue
			}
			if setPath(fields, parts, old) != nil {
				continue
			}
		} else {
			deletePath(fields, parts)
		}
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return n, writeFields(path, orig, fields)
}

// Fields reads the named settings (dotted paths) as text: a string as it is, a switch as "true" or "false", a
// setting that is not there as "". Nothing else in the file is looked at.
func Fields(path string, keys []string) (map[string]string, error) {
	_, fields, err := readFields(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, k := range keys {
		v, ok := getPath(fields, strings.Split(k, "."))
		switch x := v.(type) {
		case string:
			out[k] = x
		case bool:
			out[k] = strconv.FormatBool(x)
		case float64:
			out[k] = strconv.FormatFloat(x, 'f', -1, 64)
		default:
			out[k] = ""
			_ = ok
		}
	}
	return out, nil
}

// SetField changes one named setting. v is a string or a bool.
func SetField(path, key string, v any) error {
	orig, fields, err := readFields(path)
	if err != nil {
		return err
	}
	if err := setPath(fields, strings.Split(key, "."), v); err != nil {
		return err
	}
	return writeFields(path, orig, fields)
}
