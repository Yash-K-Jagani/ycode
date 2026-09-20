package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

type jsonStore struct{}

func (jsonStore) Backend() string { return "json" }

func (jsonStore) Save(s *Session) error {
	return saveJSON(s)
}

func (jsonStore) List() ([]Session, error) {
	entries, err := os.ReadDir(dir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir(), e.Name()))
		if err != nil {
			continue
		}
		var s Session
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (jsonStore) Load(id string) (*Session, error) {
	data, err := os.ReadFile(filepath.Join(dir(), id+".json"))
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (jsonStore) all() ([]Session, error) { return jsonStore{}.List() }
