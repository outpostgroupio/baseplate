package resources

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/provider/roblox/opencloud"
)

type fakeSecret struct {
	created, updated opencloud.SecretWrite
	deleted          string
	list             []opencloud.Secret
	createCalls      int
	deleteCalls      int
}

func (f *fakeSecret) List(context.Context, string) ([]opencloud.Secret, error) { return f.list, nil }
func (f *fakeSecret) Create(_ context.Context, _ string, w opencloud.SecretWrite) (opencloud.Secret, error) {
	f.created = w
	f.createCalls++
	return opencloud.Secret{Name: w.Name, Domain: w.Domain, KeyID: "k"}, nil
}
func (f *fakeSecret) Update(_ context.Context, _ string, w opencloud.SecretWrite) (opencloud.Secret, error) {
	f.updated = w
	return opencloud.Secret{Name: w.Name, Domain: w.Domain, KeyID: "k"}, nil
}
func (f *fakeSecret) Delete(_ context.Context, _, name string) error {
	f.deleted = name
	f.deleteCalls++
	return nil
}

func secretDesired() provider.Attributes {
	return provider.Attributes{
		"universe_id": cty.StringVal("100"),
		"name":        cty.StringVal("aws"),
		"value":       cty.StringVal("hunter2"),
		"domain":      cty.StringVal("*"),
	}
}

func TestSecretCreateDoesNotLeakValueToState(t *testing.T) {
	f := &fakeSecret{}
	id, attrs, err := UniverseSecret(f).Create(context.Background(), secretDesired())
	if err != nil {
		t.Fatal(err)
	}
	if id != "100/aws" {
		t.Fatalf("id = %q, want 100/aws", id)
	}
	if string(f.created.Value) != "hunter2" || f.created.Domain != "*" || f.created.Name != "aws" {
		t.Fatalf("client got %+v", f.created)
	}
	if _, ok := attrs["value"]; ok {
		t.Fatal("state attributes must not contain the plaintext value")
	}
	if !attrs["domain"].RawEquals(cty.StringVal("*")) {
		t.Fatalf("domain = %#v", attrs["domain"])
	}
}

func TestSecretIsSensitive(t *testing.T) {
	s, ok := UniverseSecret(&fakeSecret{}).(provider.Sensitive)
	if !ok {
		t.Fatal("roblox_universe_secret must implement provider.Sensitive")
	}
	got := s.Sensitive()
	if len(got) != 1 || got[0] != "value" {
		t.Fatalf("Sensitive() = %v, want [value]", got)
	}
}

func TestSecretTriggersHashChangesWithValue(t *testing.T) {
	tr, ok := UniverseSecret(&fakeSecret{}).(provider.Triggerable)
	if !ok {
		t.Fatal("roblox_universe_secret must implement provider.Triggerable")
	}
	d := secretDesired()
	t1, err := tr.Triggers(d)
	if err != nil {
		t.Fatal(err)
	}
	d["value"] = cty.StringVal("different")
	t2, err := tr.Triggers(d)
	if err != nil {
		t.Fatal(err)
	}
	if t1["value_hash"].RawEquals(t2["value_hash"]) {
		t.Fatal("value_hash must change when the plaintext changes")
	}
}

func TestSecretReadNotFound(t *testing.T) {
	f := &fakeSecret{list: []opencloud.Secret{{Name: "other", Domain: "*"}}}
	_, err := UniverseSecret(f).Read(context.Background(), "100/aws")
	if err != provider.ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSecretReadFound(t *testing.T) {
	f := &fakeSecret{list: []opencloud.Secret{{Name: "aws", Domain: "discord.com", KeyID: "k"}}}
	attrs, err := UniverseSecret(f).Read(context.Background(), "100/aws")
	if err != nil {
		t.Fatal(err)
	}
	if !attrs["domain"].RawEquals(cty.StringVal("discord.com")) {
		t.Fatalf("domain = %#v", attrs["domain"])
	}
	if _, ok := attrs["value"]; ok {
		t.Fatal("Read must never return a value attribute")
	}
}

func TestSecretDeleteHardDeletes(t *testing.T) {
	f := &fakeSecret{}
	if err := UniverseSecret(f).Delete(context.Background(), "100/aws"); err != nil {
		t.Fatal(err)
	}
	if f.deleteCalls != 1 || f.deleted != "aws" {
		t.Fatalf("expected a real Delete of aws, got calls=%d name=%q", f.deleteCalls, f.deleted)
	}
}
