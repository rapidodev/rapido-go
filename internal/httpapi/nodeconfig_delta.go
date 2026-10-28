package httpapi

import (
	"encoding/json"
	"time"
)

type compactWire struct {
	Version  string                          `json:"version"`
	Core     json.RawMessage                 `json:"core"`
	Inbounds []json.RawMessage               `json:"inbounds"`
	Sets     map[string][]nodeConfigUserSpec `json:"user_sets"`
}
type usersDelta struct {
	Base   string               `json:"base"`
	Upsert []nodeConfigUserSpec `json:"upsert,omitempty"`
	Remove []string             `json:"remove,omitempty"`
}

func (c *nodeConfigCache) delta(key, etag string, current *nodeConfigEntry) []byte {
	if etag == "" {
		return nil
	}
	c.mu.Lock()
	var previous *nodeConfigEntry
	for _, entry := range c.previous[key] {
		if etagMatches(etag, entry.etag) && time.Since(entry.builtAt) < nodeConfigSafetyTTL {
			previous = entry
			break
		}
	}
	c.mu.Unlock()
	if previous == nil {
		return nil
	}
	var before, after compactWire
	if json.Unmarshal(previous.body, &before) != nil || json.Unmarshal(current.body, &after) != nil {
		return nil
	}
	refs := map[string]string{}
	for _, raw := range before.Inbounds {
		var in struct {
			Tag string `json:"tag"`
			Ref string `json:"users_ref"`
		}
		if json.Unmarshal(raw, &in) == nil {
			refs[in.Tag] = in.Ref
		}
	}
	changes := map[string]usersDelta{}
	for _, raw := range after.Inbounds {
		var in struct {
			Tag string `json:"tag"`
			Ref string `json:"users_ref"`
		}
		if json.Unmarshal(raw, &in) != nil {
			return nil
		}
		if _, done := changes[in.Ref]; done {
			continue
		}
		if _, same := before.Sets[in.Ref]; same {
			changes[in.Ref] = usersDelta{Base: in.Ref}
			continue
		}
		base := refs[in.Tag]
		old := map[string]nodeConfigUserSpec{}
		for _, u := range before.Sets[base] {
			old[u.Name] = u
		}
		d := usersDelta{Base: base}
		for _, u := range after.Sets[in.Ref] {
			previous, exists := old[u.Name]
			if !exists || previous != u {
				d.Upsert = append(d.Upsert, u)
			}
			delete(old, u.Name)
		}
		for name := range old {
			d.Remove = append(d.Remove, name)
		}
		changes[in.Ref] = d
	}
	raw, err := json.Marshal(struct {
		Version  string                `json:"version"`
		Base     string                `json:"base_version"`
		Core     json.RawMessage       `json:"core"`
		Inbounds []json.RawMessage     `json:"inbounds"`
		Deltas   map[string]usersDelta `json:"user_sets_delta"`
	}{after.Version, before.Version, after.Core, after.Inbounds, changes})
	if err != nil || len(raw) >= len(current.body) {
		return nil
	}
	return raw
}
