package secrets

import (
	"github.com/AndreiD108/d108-go/doctor"
	"github.com/AndreiD108/d108-go/failure"
)

// registerDoctor adds the "secrets" section: one item per stored name with the value
// "stored", or "none" when the file is empty or absent; when the file cannot be read, the
// item "error" with the failure's message and its next step as the note.
func (s *Store) registerDoctor() {
	doctor.Add("secrets", func() []doctor.Item {
		names, err := s.Names()
		if err != nil {
			f := failure.From(err)
			return []doctor.Item{{Key: "error", Value: f.Message, Note: f.NextStep}}
		}
		if len(names) == 0 {
			return []doctor.Item{{Key: "stored", Value: "none"}}
		}
		items := make([]doctor.Item, len(names))
		for i, n := range names {
			items[i] = doctor.Item{Key: n, Value: "stored"}
		}
		return items
	})
}
