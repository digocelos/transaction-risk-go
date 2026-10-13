package evaluation

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/digocelos/transaction-risk-go/trainingdata"
)

func fraud(score float64) Prediction {
	return Prediction{Score: score, Label: trainingdata.Fraud, AmountCents: 1000}
}

func legitimate(score float64) Prediction {
	return Prediction{Score: score, Label: trainingdata.Legitimate, AmountCents: 1000}
}

func loss(cents int64) *int64 {
	return &cents
}

func mustEvaluate(t *testing.T, predictions []Prediction, threshold float64) Metrics {
	t.Helper()

	metrics, err := Evaluate(predictions, threshold)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	return metrics
}

// approx checks a defined ratio against a hand-calculated value.
func approx(t *testing.T, name string, got *float64, want float64) {
	t.Helper()

	if got == nil {
		t.Errorf("%s = null, want %v", name, want)
		return
	}

	if math.Abs(*got-want) > 1e-12 {
		t.Errorf("%s = %v, want %v", name, *got, want)
	}
}

func undefined(t *testing.T, name string, got *float64) {
	t.Helper()

	if got != nil {
		t.Errorf("%s = %v, want null", name, *got)
	}
}

func TestEvaluatePRAUCHandCalculated(t *testing.T) {
	tests := []struct {
		name        string
		predictions []Prediction
		want        float64
	}{
		// Points: (P 1, R 1/2), (1/2, 1/2), (2/3, 1), (1/2, 1).
		// AP = 1/2·1 + 0·1/2 + 1/2·2/3 + 0 = 5/6.
		{"distinct scores", []Prediction{fraud(0.9), legitimate(0.8), fraud(0.7), legitimate(0.6)}, 5.0 / 6.0},
		// The two 0.9 scores form one step: (1/2, 1/2), then (2/3, 1).
		// AP = 1/2·1/2 + 1/2·2/3 = 7/12, whatever the order within the tie.
		{"tied scores form one step", []Prediction{fraud(0.9), legitimate(0.9), fraud(0.5), legitimate(0.1)}, 7.0 / 12.0},
		{"all correct", []Prediction{fraud(0.9), fraud(0.8), legitimate(0.2), legitimate(0.1)}, 1},
		// Points: (0, 0), (0, 0), (1/3, 1/2), (1/2, 1). AP = 1/6 + 1/4 = 5/12.
		{"all wrong", []Prediction{fraud(0.1), fraud(0.2), legitimate(0.8), legitimate(0.9)}, 5.0 / 12.0},
		// Points: (1, 1/2), (1, 1). AP = 1.
		{"fraud only", []Prediction{fraud(0.9), fraud(0.1)}, 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			approx(t, "pr_auc", mustEvaluate(t, test.predictions, 0.5).PRAUC, test.want)
		})
	}
}

func TestEvaluatePRCurve(t *testing.T) {
	metrics := mustEvaluate(t, []Prediction{legitimate(0.1), fraud(0.5), legitimate(0.9), fraud(0.9)}, 0.5)

	want := []struct {
		threshold, precision, recall float64
	}{
		{0.9, 0.5, 0.5},
		{0.5, 2.0 / 3.0, 1},
		{0.1, 0.5, 1},
	}

	if len(metrics.PRCurve) != len(want) {
		t.Fatalf("PRCurve has %d points, want %d: %+v", len(metrics.PRCurve), len(want), metrics.PRCurve)
	}

	for i, point := range metrics.PRCurve {
		if point.Threshold != want[i].threshold || math.Abs(point.Precision-want[i].precision) > 1e-12 {
			t.Errorf("point %d = %+v, want threshold %v and precision %v", i, point, want[i].threshold, want[i].precision)
		}
		approx(t, "recall", point.Recall, want[i].recall)
	}
}

func TestEvaluateConfusionMatrixAndRatios(t *testing.T) {
	tests := []struct {
		name                                       string
		predictions                                []Prediction
		threshold                                  float64
		tp, fp, tn, fn                             int
		precision, recall, f1, fpr, fpPer1000Legit float64
	}{
		{"all correct", []Prediction{fraud(0.9), fraud(0.8), legitimate(0.2), legitimate(0.1)}, 0.5,
			2, 0, 2, 0, 1, 1, 1, 0, 0},
		{"all wrong", []Prediction{fraud(0.1), fraud(0.2), legitimate(0.8), legitimate(0.9)}, 0.5,
			0, 2, 0, 2, 0, 0, 0, 1, 1000},
		{"mixed", []Prediction{fraud(0.9), legitimate(0.8), fraud(0.7), legitimate(0.6)}, 0.75,
			1, 1, 1, 1, 0.5, 0.5, 0.5, 0.5, 500},
		{"a score equal to the threshold is fraud", []Prediction{fraud(0.5), legitimate(0.5)}, 0.5,
			1, 1, 0, 0, 0.5, 1, 2.0 / 3.0, 1, 1000},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := mustEvaluate(t, test.predictions, test.threshold)

			if m.TruePositives != test.tp || m.FalsePositives != test.fp ||
				m.TrueNegatives != test.tn || m.FalseNegatives != test.fn {
				t.Errorf("confusion = TP %d FP %d TN %d FN %d, want %d %d %d %d",
					m.TruePositives, m.FalsePositives, m.TrueNegatives, m.FalseNegatives,
					test.tp, test.fp, test.tn, test.fn)
			}

			approx(t, "precision", m.Precision, test.precision)
			approx(t, "recall", m.Recall, test.recall)
			approx(t, "f1", m.F1, test.f1)
			approx(t, "false_positive_rate", m.FalsePositiveRate, test.fpr)
			approx(t, "false_positives_per_1000_legitimate", m.FalsePositivesPer1000Legitimate, test.fpPer1000Legit)
		})
	}
}

func TestEvaluateUndefinedRatiosAreNull(t *testing.T) {
	t.Run("empty partition", func(t *testing.T) {
		m := mustEvaluate(t, nil, 0.5)

		for name, ratio := range map[string]*float64{
			"precision": m.Precision, "recall": m.Recall, "f1": m.F1,
			"false_positive_rate": m.FalsePositiveRate, "per_1000": m.FalsePositivesPer1000Legitimate,
			"pr_auc": m.PRAUC,
		} {
			undefined(t, name, ratio)
		}

		if m.PRCurve == nil || len(m.PRCurve) != 0 {
			t.Errorf("PRCurve = %#v, want an empty curve", m.PRCurve)
		}
	})

	t.Run("legitimate only, none flagged", func(t *testing.T) {
		m := mustEvaluate(t, []Prediction{legitimate(0.1), legitimate(0.2)}, 0.5)

		undefined(t, "precision", m.Precision)
		undefined(t, "recall", m.Recall)
		undefined(t, "f1", m.F1)
		undefined(t, "pr_auc", m.PRAUC)
		approx(t, "false_positive_rate", m.FalsePositiveRate, 0)
		approx(t, "false_positives_per_1000_legitimate", m.FalsePositivesPer1000Legitimate, 0)

		for _, point := range m.PRCurve {
			undefined(t, "curve recall", point.Recall)
		}
	})

	t.Run("legitimate only, one flagged", func(t *testing.T) {
		m := mustEvaluate(t, []Prediction{legitimate(0.9)}, 0.5)

		approx(t, "precision", m.Precision, 0)
		undefined(t, "recall", m.Recall)
		approx(t, "f1", m.F1, 0)
		approx(t, "false_positive_rate", m.FalsePositiveRate, 1)
	})

	t.Run("fraud only", func(t *testing.T) {
		m := mustEvaluate(t, []Prediction{fraud(0.9), fraud(0.1)}, 0.5)

		approx(t, "precision", m.Precision, 1)
		approx(t, "recall", m.Recall, 0.5)
		approx(t, "f1", m.F1, 2.0/3.0)
		undefined(t, "false_positive_rate", m.FalsePositiveRate)
		undefined(t, "false_positives_per_1000_legitimate", m.FalsePositivesPer1000Legitimate)
	})
}

func TestEvaluateAmountsKeepUnknownLossApart(t *testing.T) {
	predictions := []Prediction{
		{Score: 0.9, Label: trainingdata.Fraud, AmountCents: 1000, LossAmountCents: loss(600)},
		{Score: 0.8, Label: trainingdata.Fraud, AmountCents: 500},
		{Score: 0.2, Label: trainingdata.Fraud, AmountCents: 300, LossAmountCents: loss(300)},
		{Score: 0.1, Label: trainingdata.Fraud, AmountCents: 200},
		{Score: 0.7, Label: trainingdata.Legitimate, AmountCents: 700, LossAmountCents: loss(0)},
		{Score: 0.3, Label: trainingdata.Legitimate, AmountCents: 100, LossAmountCents: loss(0)},
	}

	m := mustEvaluate(t, predictions, 0.5)

	got := []int64{
		m.FraudAmountCentsDetected, m.FraudAmountCentsMissed, m.LegitimateAmountCentsFlagged,
		m.KnownLossCentsDetected, m.KnownLossCentsMissed,
		int64(m.UnknownLossFraudsDetected), int64(m.UnknownLossFraudsMissed),
	}
	want := []int64{1500, 500, 700, 600, 300, 1, 1}

	if !slices.Equal(got, want) {
		t.Errorf("amounts = %v, want %v", got, want)
	}

	unknownOnly := mustEvaluate(t, []Prediction{
		{Score: 0.9, Label: trainingdata.Fraud, AmountCents: 1000},
		{Score: 0.1, Label: trainingdata.Fraud, AmountCents: 1000},
	}, 0.5)

	if unknownOnly.KnownLossCentsDetected != 0 || unknownOnly.KnownLossCentsMissed != 0 ||
		unknownOnly.UnknownLossFraudsDetected != 1 || unknownOnly.UnknownLossFraudsMissed != 1 {
		t.Errorf("unknown losses = %+v, want them counted apart from known losses", unknownOnly)
	}
}

func TestEvaluateIgnoresInputOrder(t *testing.T) {
	predictions := []Prediction{
		{Score: 0.9, Label: trainingdata.Fraud, AmountCents: 1000, LossAmountCents: loss(600)},
		{Score: 0.9, Label: trainingdata.Legitimate, AmountCents: 700},
		{Score: 0.5, Label: trainingdata.Fraud, AmountCents: 300},
		{Score: 0.5, Label: trainingdata.Legitimate, AmountCents: 100},
		{Score: 0.1, Label: trainingdata.Fraud, AmountCents: 200, LossAmountCents: loss(200)},
	}

	want := mustEvaluate(t, predictions, 0.5)

	reversed := slices.Clone(predictions)
	slices.Reverse(reversed)
	rotated := append(slices.Clone(predictions[2:]), predictions[:2]...)

	for _, order := range [][]Prediction{reversed, rotated} {
		if got := mustEvaluate(t, order, 0.5); !reflect.DeepEqual(got, want) {
			t.Errorf("metrics changed with input order:\n%+v\n%+v", got, want)
		}
	}
}

func TestEvaluateJSONUsesNullForUndefined(t *testing.T) {
	data, err := json.Marshal(mustEvaluate(t, nil, 0.5))
	if err != nil {
		t.Fatal(err)
	}

	want := `{"threshold":0.5,"true_positives":0,"false_positives":0,"true_negatives":0,"false_negatives":0,` +
		`"precision":null,"recall":null,"f1":null,"false_positive_rate":null,` +
		`"false_positives_per_1000_legitimate":null,"pr_auc":null,"pr_curve":[],` +
		`"fraud_amount_cents_detected":0,"fraud_amount_cents_missed":0,"legitimate_amount_cents_flagged":0,` +
		`"known_loss_cents_detected":0,"known_loss_cents_missed":0,` +
		`"unknown_loss_frauds_detected":0,"unknown_loss_frauds_missed":0}`

	if string(data) != want {
		t.Errorf("JSON = %s\nwant   %s", data, want)
	}

	data, err = json.Marshal(mustEvaluate(t, []Prediction{legitimate(0.9), legitimate(0.1)}, 0.5))
	if err != nil {
		t.Fatalf("metrics with undefined ratios must encode: %v", err)
	}
	if strings.Contains(string(data), "NaN") || strings.Contains(string(data), "Inf") {
		t.Errorf("JSON = %s, want no NaN or infinity", data)
	}
}

func TestEvaluateRejectsInvalidInput(t *testing.T) {
	const largest = 1<<53 - 1

	tests := map[string]struct {
		predictions []Prediction
		threshold   float64
	}{
		"NaN threshold":      {nil, math.NaN()},
		"negative threshold": {nil, -0.01},
		"threshold above 1":  {nil, 1.01},
		"infinite threshold": {nil, math.Inf(1)},
		"NaN score":          {[]Prediction{fraud(math.NaN())}, 0.5},
		"infinite score":     {[]Prediction{fraud(math.Inf(1))}, 0.5},
		"negative score":     {[]Prediction{fraud(-0.1)}, 0.5},
		"score above 1":      {[]Prediction{fraud(1.1)}, 0.5},
		"unknown label":      {[]Prediction{{Score: 0.5, Label: "chargeback", AmountCents: 1}}, 0.5},
		"negative amount":    {[]Prediction{{Score: 0.5, Label: trainingdata.Fraud, AmountCents: -1}}, 0.5},
		"negative loss":      {[]Prediction{{Score: 0.5, Label: trainingdata.Fraud, AmountCents: 1, LossAmountCents: loss(-1)}}, 0.5},
		"amount sum overflow": {[]Prediction{
			{Score: 0.9, Label: trainingdata.Fraud, AmountCents: largest},
			{Score: 0.9, Label: trainingdata.Fraud, AmountCents: 1},
		}, 0.5},
	}

	for name, test := range tests {
		if _, err := Evaluate(test.predictions, test.threshold); err == nil {
			t.Errorf("%s: Evaluate() error = nil, want an error", name)
		}
	}
}
