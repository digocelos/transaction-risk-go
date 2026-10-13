package pixinput

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const examplesDir = "../docs/contracts/examples"

func readExample(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(examplesDir, name))
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()

	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}

	return parsed
}

func assertInstant(t *testing.T, name string, got time.Time, want string) {
	t.Helper()

	if !got.Equal(mustTime(t, want)) {
		t.Errorf("%s = %s, want %s", name, got.Format(time.RFC3339Nano), want)
	}
}

func assertWindow(t *testing.T, name string, got *WindowCounter, d90, m12, m60 int64) {
	t.Helper()

	if got == nil || got.D90 == nil || got.M12 == nil || got.M60 == nil {
		t.Fatalf("%s = %+v, want all windows known", name, got)
	}

	if *got.D90 != d90 || *got.M12 != m12 || *got.M60 != m60 {
		t.Errorf(
			"%s = {%d %d %d}, want {%d %d %d}",
			name, *got.D90, *got.M12, *got.M60, d90, m12, m60,
		)
	}
}

func TestDecodeCompleteExample(t *testing.T) {
	input, err := Decode(readExample(t, "01-complete.json"))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if input.ContractVersion != "pix-payer-input-v1" {
		t.Errorf("ContractVersion = %q", input.ContractVersion)
	}

	assertInstant(t, "DecisionTime", input.DecisionTime, "2026-10-13T17:03:07.120Z")

	if _, offset := input.DecisionTime.Zone(); offset != -3*60*60 {
		t.Errorf("DecisionTime offset = %d, want the received -03:00", offset)
	}

	payment := input.PixPayment
	if payment.AmountCents != 250000 ||
		payment.PayerDeviceUsage != PayerDevice ||
		payment.RecipientKeyUsage != PixKey ||
		payment.RecipientPersonType != NaturalPerson {
		t.Errorf("PixPayment = %+v", payment)
	}

	observations := input.Observations

	settled := observations.PayerAccount.SettledOutgoingPix90d
	if settled == nil || settled.Count != 31 ||
		settled.MedianAmountCents == nil || *settled.MedianAmountCents != 25000 {
		t.Fatalf("SettledOutgoingPix90d = %+v", settled)
	}
	assertInstant(t, "settled as_of", settled.AsOf, "2026-10-13T06:00:00Z")

	initiated := observations.PayerAccount.InitiatedOutgoingPix24h
	if initiated == nil || initiated.Count != 2 {
		t.Fatalf("InitiatedOutgoingPix24h = %+v", initiated)
	}
	assertInstant(t, "initiated as_of", initiated.AsOf, "2026-10-13T17:03:06.874Z")

	device := observations.PayerDevice
	if device == nil || device.SettledOutgoingPix90d == nil ||
		device.SettledOutgoingPix90d.Count != 14 {
		t.Fatalf("PayerDevice = %+v", device)
	}

	person := observations.RecipientPerson
	if person.SettledPixFromPayerAccount365d == nil ||
		person.SettledPixFromPayerAccount365d.Count != 0 {
		t.Fatalf("SettledPixFromPayerAccount365d = %+v", person.SettledPixFromPayerAccount365d)
	}
	if person.Spi == nil || person.FraudMarkers == nil ||
		person.InfractionReports == nil || person.Entries == nil {
		t.Fatalf("RecipientPerson statistics = %+v", person)
	}
	assertWindow(t, "person settlements", person.Spi.Settlements, 12, 40, 95)
	assertWindow(t, "person mule accounts", person.FraudMarkers.MuleAccounts, 0, 0, 0)
	assertWindow(t, "person rejected reports", person.InfractionReports.RejectedReports, 0, 0, 0)
	if person.InfractionReports.OpenReports == nil || *person.InfractionReports.OpenReports != 0 {
		t.Errorf("OpenReports = %v, want known 0", person.InfractionReports.OpenReports)
	}
	if person.Entries.RegisteredAccounts == nil || *person.Entries.RegisteredAccounts != 2 {
		t.Errorf("RegisteredAccounts = %v, want 2", person.Entries.RegisteredAccounts)
	}
	assertInstant(t, "person spi as_of", person.Spi.AsOf, "2026-10-13T17:03:06.105Z")

	account := observations.RecipientAccount.Entry
	if account == nil {
		t.Fatal("RecipientAccount.Entry = nil")
	}
	assertInstant(t, "opening_date", account.OpeningDate, "2019-05-14T03:00:00Z")
	assertInstant(t, "account as_of", account.AsOf, "2026-10-13T17:03:05.912Z")

	key := observations.RecipientPixKey
	if key == nil || key.Entry == nil || key.Spi == nil || key.Entries == nil {
		t.Fatalf("RecipientPixKey = %+v", key)
	}
	if key.Entry.KeyType != "EVP" {
		t.Errorf("KeyType = %q", key.Entry.KeyType)
	}
	assertInstant(t, "creation_date", key.Entry.CreationDate, "2025-07-19T10:02:45.318Z")
	assertInstant(t, "key_ownership_date", key.Entry.KeyOwnershipDate, "2023-02-08T19:41:12.004Z")
	assertWindow(t, "key settlements", key.Spi.Settlements, 9, 31, 70)
	assertWindow(t, "key distinct accounts", key.Entries.DistinctAccounts, 1, 1, 2)
}
