package trainingdata

// DatasetReportVersion is the version of the dataset report layout.
const DatasetReportVersion = "pix-training-dataset-report-v1"

// Counts holds the number of records in each category.
type Counts struct {
	Total        int `json:"total"`
	Usable       int `json:"usable"`
	Invalid      int `json:"invalid"`
	Insufficient int `json:"insufficient"`
	Immature     int `json:"immature"`
}

// LabelCounts holds the number of usable records with each label.
type LabelCounts struct {
	Fraud      int `json:"fraud"`
	Legitimate int `json:"legitimate"`
}

// Summary describes a scanned dataset without listing its records.
type Summary struct {
	FeatureSchema string
	DatasetSHA256 string
	DatasetBytes  int64
	Records       Counts
	UsableLabels  LabelCounts
}

func (s *Summary) add(record Record) {
	s.Records.Total++

	switch record.Category {
	case Usable:
		s.Records.Usable++
		if record.Example.Outcome.Label == Fraud {
			s.UsableLabels.Fraud++
		} else {
			s.UsableLabels.Legitimate++
		}
	case Invalid:
		s.Records.Invalid++
	case Insufficient:
		s.Records.Insufficient++
	case Immature:
		s.Records.Immature++
	}
}

// IssueReference identifies a diagnostic in a report, without its message.
type IssueReference struct {
	Path string `json:"path"`
	Code string `json:"code"`
}

// Exclusion records a record that is not used to train or evaluate.
type Exclusion struct {
	Line     int              `json:"line"`
	Category Category         `json:"category"`
	Issues   []IssueReference `json:"issues,omitempty"`
	Blockers []Blocker        `json:"blockers,omitempty"`
}

// Exclusion returns the exclusion entry of a record that is not usable.
func (r Record) Exclusion() (Exclusion, bool) {
	if r.Category == Usable {
		return Exclusion{}, false
	}

	exclusion := Exclusion{Line: r.Line, Category: r.Category, Blockers: r.Blockers}
	for _, diagnostic := range r.Diagnostics {
		exclusion.Issues = append(exclusion.Issues, IssueReference{
			Path: diagnostic.Path,
			Code: diagnostic.Code,
		})
	}

	return exclusion, true
}

// Report is the dataset report of the pix-training-example-v1 contract.
type Report struct {
	DatasetReportVersion   string      `json:"dataset_report_version"`
	TrainingExampleVersion string      `json:"training_example_version"`
	FeatureSchema          string      `json:"feature_schema"`
	LabelMaturityDays      int         `json:"label_maturity_days"`
	DatasetSHA256          string      `json:"dataset_sha256"`
	DatasetBytes           int64       `json:"dataset_bytes"`
	Records                Counts      `json:"records"`
	UsableLabels           LabelCounts `json:"usable_labels"`
	Exclusions             []Exclusion `json:"exclusions"`
}

// NewReport builds the dataset report from a summary and the exclusions of
// its records, in line order.
func NewReport(summary Summary, exclusions []Exclusion) Report {
	if exclusions == nil {
		exclusions = []Exclusion{}
	}

	return Report{
		DatasetReportVersion:   DatasetReportVersion,
		TrainingExampleVersion: TrainingExampleVersion,
		FeatureSchema:          summary.FeatureSchema,
		LabelMaturityDays:      int(LabelMaturity.Hours() / 24),
		DatasetSHA256:          summary.DatasetSHA256,
		DatasetBytes:           summary.DatasetBytes,
		Records:                summary.Records,
		UsableLabels:           summary.UsableLabels,
		Exclusions:             exclusions,
	}
}
