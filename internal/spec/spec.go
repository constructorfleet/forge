package spec

import (
	"github.com/Teagan42/forge/internal/planning"
)

const (
	KindSpecification = "specification"
)

var RequiredSections = []string{
	"Context",
	"Requirements",
	"Non-Goals",
}

type Specification struct {
	planning.Artifact
}

func NewSpecification() *Specification {
	return &Specification{
		Artifact: planning.Artifact{
			Kind:     planning.KindSpec,
			Sections: make([]planning.Section, 0, len(RequiredSections)),
		},
	}
}

func (s *Specification) AddSection(heading, body string) {
	s.Sections = append(s.Sections, planning.Section{Heading: heading, Body: body})
}
