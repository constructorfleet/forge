package planning

// metaBlock is the YAML shape of the `<!-- forge ... -->` block. Field
// order here is the canonical render order: workflow fields first, then
// the definitional derived_from list.
type metaBlock struct {
	Revision         string        `yaml:"revision"`
	State            string        `yaml:"state"`
	ApprovedRevision string        `yaml:"approved_revision"`
	ApprovedBy       string        `yaml:"approved_by"`
	ApprovedAt       string        `yaml:"approved_at"`
	Kind             string        `yaml:"kind"`
	DerivedFrom      []derivedYAML `yaml:"derived_from,omitempty"`
}

type derivedYAML struct {
	Kind     string `yaml:"kind"`
	ID       string `yaml:"id"`
	Revision string `yaml:"revision"`
}

const (
	blockOpen  = "<!-- forge"
	blockClose = "-->"
)
