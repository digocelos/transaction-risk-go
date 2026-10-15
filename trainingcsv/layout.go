// Package trainingcsv converts training data prepared in a spreadsheet,
// in the CSV layout of docs/contracts/pix-training-csv-v1.md, into
// pix-training-example-v2 JSON Lines records that train and evaluate read
// (ADR 0021). Every record is validated with the strict
// pix-training-example-v2 decoder, and problems are reported by row and
// column.
package trainingcsv

import (
	"slices"
	"strings"
)

// kind is the type of a leaf column, or the role of an inner node.
type kind int

const (
	integer kind = iota
	text
	object
	window
)

// node describes one member of a training example. A leaf is a column; an
// object holds members; a window holds the counters d90, m12, and m60.
//
// An observation is an object whose presence column, its as_of, decides
// whether it is null. A conditional member is omitted when the
// discriminator column holds the value omitWhen.
type node struct {
	name     string
	kind     kind
	children []node
	values   string // a description of the accepted values, for the guide
	empty    string // what an empty cell means, for the guide

	observation   bool
	discriminator string
	omitWhen      string
}

const (
	emptyInvalid  = "Invalid: the value is required."
	emptyMissing  = "Missing: the value is not known."
	emptyAbsent   = "The observation is missing, and every other column of it must be empty."
	timestampDesc = "Timestamp: RFC 3339 with an offset, such as `2026-05-14T14:03:07.120-03:00`."
	countDesc     = "Integer from 0."
	centsDesc     = "Integer amount in cents."
)

func leaf(name string, k kind, values, empty string) node {
	return node{name: name, kind: k, values: values, empty: empty}
}

func asOf() node {
	return leaf("as_of", text, timestampDesc, emptyAbsent)
}

func observed(name string, children ...node) node {
	return node{name: name, kind: object, observation: true, children: append(children, asOf())}
}

func counters(name string) node {
	return node{name: name, kind: window, children: []node{
		leaf("d90", integer, countDesc, emptyMissing),
		leaf("m12", integer, countDesc, emptyMissing),
		leaf("m60", integer, countDesc, emptyMissing),
	}}
}

func statistics() []node {
	return []node{
		observed("spi", counters("settlements")),
		observed("fraud_markers",
			counters("application_frauds"), counters("mule_accounts"), counters("scammer_accounts"),
			counters("other_frauds"), counters("unknown_frauds"), counters("distinct_fraud_reporters")),
		observed("infraction_reports",
			leaf("open_reports", integer, countDesc, emptyMissing),
			leaf("open_reports_distinct_reporters", integer, countDesc, emptyMissing),
			counters("rejected_reports")),
	}
}

// record is the layout of a pix-training-example-v2 record, in the order
// of the contract. training_example_version and contract_version are
// implied by the layout and have no column.
var record = node{kind: object, children: []node{
	{name: "scoring_input", kind: object, children: []node{
		leaf("decision_time", text, timestampDesc, emptyInvalid),
		{name: "pix_payment", kind: object, children: []node{
			leaf("amount_cents", integer, "Integer amount in cents, from 1.", emptyInvalid),
			leaf("payer_device_usage", text, "`payer_device` or `no_payer_device`.", emptyInvalid),
			leaf("recipient_key_usage", text, "`pix_key` or `no_pix_key`.", emptyInvalid),
			leaf("recipient_person_type", text, "`NATURAL_PERSON` or `LEGAL_PERSON`.", emptyInvalid),
			leaf("reference_time_offset", text, "Offset `±hh:mm`, such as `-03:00`.", emptyInvalid),
			leaf("payer_limit_period", text, "`daytime`, `nighttime`, `daily`, or `no_period_limit`.", emptyInvalid),
		}},
		{name: "observations", kind: object, children: []node{
			{name: "payer_account", kind: object, children: []node{
				observed("settled_outgoing_pix_90d",
					leaf("count", integer, countDesc, emptyInvalid),
					leaf("median_amount_cents", integer, centsDesc, "Required when count is above 0; empty when count is 0.")),
				observed("initiated_outgoing_pix_24h", leaf("count", integer, countDesc, emptyInvalid)),
				withCondition(observed("period_limit",
					leaf("limit_cents", integer, centsDesc+" 0 is a zero limit.", emptyInvalid),
					leaf("consumed_cents", integer, centsDesc, emptyInvalid)),
					"pix_payment.payer_limit_period", "no_period_limit"),
			}},
			withCondition(node{name: "payer_device", kind: object, children: []node{
				observed("settled_outgoing_pix_90d", leaf("count", integer, countDesc, emptyInvalid)),
				observed("registration", leaf("status", text, "`registered` or `not_registered`.", emptyInvalid)),
			}}, "pix_payment.payer_device_usage", "no_payer_device"),
			{name: "recipient_person", kind: object, children: append([]node{
				observed("settled_pix_from_payer_account_365d", leaf("count", integer, countDesc, emptyInvalid)),
			}, append(statistics(),
				observed("entries", leaf("registered_accounts", integer, countDesc, emptyMissing)))...)},
			{name: "recipient_account", kind: object, children: []node{
				observed("entry", leaf("opening_date", text, timestampDesc, emptyInvalid)),
				observed("pre_registration", leaf("status", text, "`registered` or `not_registered`.", emptyInvalid)),
			}},
			withCondition(node{name: "recipient_pix_key", kind: object, children: append([]node{
				observed("entry",
					leaf("key_type", text, "`CPF`, `CNPJ`, `PHONE`, `EMAIL`, `EVP`, or another type in capitals.", emptyInvalid),
					leaf("creation_date", text, timestampDesc, emptyInvalid),
					leaf("key_ownership_date", text, timestampDesc, emptyInvalid)),
			}, append(statistics(), observed("entries", counters("distinct_accounts")))...)},
				"pix_payment.recipient_key_usage", "no_pix_key"),
		}},
	}},
	{name: "outcome", kind: object, children: []node{
		leaf("label", text, "`fraud` or `legitimate`.", emptyInvalid),
		leaf("label_as_of", text, timestampDesc+" When the label is known.", emptyInvalid),
		leaf("loss_amount_cents", integer, centsDesc+" 0 for a legitimate payment.", "Unknown loss, allowed for a fraud only."),
	}},
	leaf("group_key", text, "Up to 64 letters, digits, `_`, or `-`; never a personal identifier.", "No group key."),
}}

func withCondition(n node, discriminator, omitWhen string) node {
	n.discriminator, n.omitWhen = discriminator, omitWhen
	return n
}

// Column is one column of the layout, in template order.
type Column struct {
	Name   string
	Values string
	Empty  string
}

// columnName drops the scoring_input prefix, which every scoring input
// column would repeat.
func columnName(path []string) string {
	if len(path) > 0 && path[0] == "scoring_input" {
		path = path[1:]
	}
	return strings.Join(path, ".")
}

// Columns returns the columns of the layout, in template order.
func Columns() []Column {
	var columns []Column
	var walk func(n node, path []string)
	walk = func(n node, path []string) {
		switch n.kind {
		case object, window:
			for _, child := range n.children {
				walk(child, append(slices.Clip(path), child.name))
			}
		default:
			columns = append(columns, Column{Name: columnName(path), Values: n.values, Empty: n.empty})
		}
	}
	walk(record, nil)
	return columns
}
