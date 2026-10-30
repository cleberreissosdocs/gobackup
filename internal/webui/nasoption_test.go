package webui

import (
	"strings"
	"testing"
)

// "NAS (montado)" was only ever a label. Every code path treats it
// identically to "local" (case core.DestinationLocal,
// core.DestinationNAS) and builds the same rsync command, so offering it
// invited the operator to believe the system did something special for a
// network share. On Windows it actively misled: the answer there is the
// network-folder credentials, because a service cannot see a mounted
// drive letter at all.
func TestCreateFormDoesNotOfferNAS(t *testing.T) {
	create, _ := renderFileForms(t, "linux", FileJobRequest{})

	if strings.Contains(create, `value="nas"`) {
		t.Error("o formulário de criação não deveria mais oferecer NAS")
	}
	for _, want := range []string{`value="local"`, `value="ssh"`} {
		if !strings.Contains(create, want) {
			t.Errorf("o destino %s deveria continuar disponível", want)
		}
	}
}

// A job already saved as "nas" must keep showing its real value. If the
// option simply vanished from its edit form, the browser would fall back
// to the first option, and merely opening the job and pressing save would
// silently rewrite its destination type.
func TestEditFormKeepsNASOnlyForJobsAlreadyUsingIt(t *testing.T) {
	_, editNAS := renderFileForms(t, "linux", FileJobRequest{DestType: "nas"})
	if !strings.Contains(editNAS, `value="nas" selected`) {
		t.Error("uma tarefa que já usa NAS precisa continuar com NAS selecionado na edição")
	}

	_, editLocal := renderFileForms(t, "linux", FileJobRequest{DestType: "local"})
	if strings.Contains(editLocal, `value="nas"`) {
		t.Error("uma tarefa local não deveria oferecer NAS na edição")
	}
}
