package planning

// TicketEstimate holds the relative effort/complexity estimate for a ticket.
type TicketEstimate struct {
	Size string `yaml:"size"`
	Risk string `yaml:"risk,omitempty"`
}

// metaBlock is the YAML shape of the `<!-- forge ... -->` block. Field
// order here is the canonical render order: workflow fields first, then
// the definitional derived_from list.
type metaBlock struct {
	Revision         string                    `yaml:"revision"`
	State            string                    `yaml:"state"`
	ApprovedRevision string                    `yaml:"approved_revision"`
	ApprovedBy       string                    `yaml:"approved_by"`
	ApprovedAt       string                    `yaml:"approved_at"`
	ReviewedRevision string                    `yaml:"reviewed_revision"`
	Kind             string                    `yaml:"kind"`
	DerivedFrom      []derivedYAML             `yaml:"derived_from,omitempty"`
	Estimates        map[string]TicketEstimate `yaml:"estimates,omitempty"`
	TicketKinds      map[string]TicketKind     `yaml:"ticket_kinds,omitempty"`
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

// ValidEstimateSizes is the set of allowed estimate size values.
var ValidEstimateSizes = map[string]bool{
	"S":  true,
	"M":  true,
	"L":  true,
	"XL": true,
}
