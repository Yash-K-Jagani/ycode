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
		s.MessageCount = len(s.Messages)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// ListMeta for the JSON backend still has to read each file, because the whole
// session is one JSON document and there is no column to leave out. The
// messages are dropped straight after decoding, along with the count, so a
// caller that needs the transcript is using List and one that does not is not
// holding a few hundred sessions' worth of messages while it filters.
func (jsonStore) ListMeta() ([]Session, error) {
	list, err := jsonStore{}.List()
	for i := range list {
		list[i].MessageCount = len(list[i].Messages)
		list[i].Messages = nil
	}
	return list, err
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
	s.MessageCount = len(s.Messages)
	return &s, nil
}

func (jsonStore) Delete(id string) error {
	err := os.Remove(filepath.Join(dir(), id+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (jsonStore) all() ([]Session, error) { return jsonStore{}.List() }
