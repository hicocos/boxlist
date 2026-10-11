package derivatives

import (
	"context"
	"testing"
)

func TestMutationRevokesWithoutRuleOrRemoteIO(t *testing.T) {
	m := coreManager(t)
	s := coreStorage(t, m)
	a := coreArtifact(t, m, s, "/pictures/100%/one.png", "r", "thumb")
	b := coreArtifact(t, m, s, "/pictures/100x/one.png", "r", "thumb")
	if e := m.Mutated(context.Background(), s.ID, "/pictures/100%"); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Artifact(a.ID, "thumb"); e == nil {
		t.Fatal("mutated artifact served")
	}
	if _, e := m.Artifact(b.ID, "thumb"); e != nil {
		t.Fatal("wildcard filename revoked sibling", e)
	}
	select {
	case parent := <-m.events:
		if parent != "/pictures" {
			t.Fatal(parent)
		}
	default:
		t.Fatal("no cheap discovery event")
	}
}
