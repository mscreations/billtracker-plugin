// Copyright (C) 2026 Jon Shaulis
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package connectors

import (
	"context"
	"testing"
	"time"
)

type fakeConnector struct {
	slug string
}

func (f fakeConnector) Slug() string { return f.slug }

func (f fakeConnector) FetchBill(ctx context.Context, creds Credentials) (BillSnapshot, error) {
	return BillSnapshot{
		AmountCents:   1234,
		DueDate:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		AccountNumber: creds.Username,
	}, nil
}

func TestRegisterAndGet(t *testing.T) {
	c := fakeConnector{slug: "fake-connector-test"}
	Register(c)

	got, err := Get("fake-connector-test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Slug() != "fake-connector-test" {
		t.Errorf("Slug() = %q, want %q", got.Slug(), "fake-connector-test")
	}

	snap, err := got.FetchBill(context.Background(), Credentials{Username: "alice"})
	if err != nil {
		t.Fatalf("FetchBill: %v", err)
	}
	if snap.AccountNumber != "alice" {
		t.Errorf("AccountNumber = %q, want alice", snap.AccountNumber)
	}
}

func TestGetUnknownSlugReturnsError(t *testing.T) {
	_, err := Get("no-such-connector-registered")
	if err == nil {
		t.Fatal("expected an error for an unregistered slug")
	}
}
