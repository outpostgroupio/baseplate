package opencloud

import (
	"context"
	"net/http"
	"net/url"
)

// Secret is the domain view of a universe secret. The plaintext value is never
// returned by the API and is never a field here.
type Secret struct {
	Name       string // the API "id" (the user-chosen secret name)
	Domain     string
	KeyID      string
	CreateTime string
	UpdateTime string
}

// SecretWrite is the input to Create/Update. Value is the plaintext; the client
// seals it with the universe public key before upload, so the plaintext never
// leaves this process in the clear.
type SecretWrite struct {
	Name   string
	Value  []byte
	Domain string
}

// SecretClient manages a universe's Open Cloud secrets. There is no single-GET
// endpoint, so callers read via List and find by name.
type SecretClient interface {
	List(ctx context.Context, universeID string) ([]Secret, error)
	Create(ctx context.Context, universeID string, w SecretWrite) (Secret, error)
	Update(ctx context.Context, universeID string, w SecretWrite) (Secret, error)
	Delete(ctx context.Context, universeID, name string) error
}

func NewSecretClient(c *Client) SecretClient { return &secretClient{c: c} }

type secretClient struct{ c *Client }

// secretWire is the on-the-wire Secret. Domain has no omitempty so a cleared
// domain is sent explicitly (empty/null domain = private key per the API).
type secretWire struct {
	ID         string `json:"id,omitempty"`
	Secret     string `json:"secret,omitempty"`
	KeyID      string `json:"key_id,omitempty"`
	Domain     string `json:"domain"`
	CreateTime string `json:"create_time,omitempty"`
	UpdateTime string `json:"update_time,omitempty"`
}

func (w secretWire) toDomain() Secret {
	return Secret{Name: w.ID, Domain: w.Domain, KeyID: w.KeyID, CreateTime: w.CreateTime, UpdateTime: w.UpdateTime}
}

type secretListWire struct {
	Secrets        []secretWire `json:"secrets"`
	NextPageCursor string       `json:"nextPageCursor"`
}

func secretsBase(universeID string) string {
	return "/cloud/v2/universes/" + url.PathEscape(universeID) + "/secrets"
}

func (sc *secretClient) publicKey(ctx context.Context, universeID string) (pubB64, keyID string, err error) {
	var w secretWire
	if _, err := sc.c.Do(ctx, http.MethodGet, secretsBase(universeID)+"/public-key", nil, &w); err != nil {
		return "", "", err
	}
	return w.Secret, w.KeyID, nil
}

func (sc *secretClient) List(ctx context.Context, universeID string) ([]Secret, error) {
	var out []Secret
	cursor := ""
	for {
		path := secretsBase(universeID)
		if cursor != "" {
			path += "?cursor=" + url.QueryEscape(cursor)
		}
		var w secretListWire
		if _, err := sc.c.Do(ctx, http.MethodGet, path, nil, &w); err != nil {
			return nil, err
		}
		for _, s := range w.Secrets {
			out = append(out, s.toDomain())
		}
		if w.NextPageCursor == "" {
			return out, nil
		}
		cursor = w.NextPageCursor
	}
}

func (sc *secretClient) Create(ctx context.Context, universeID string, wr SecretWrite) (Secret, error) {
	body, err := sc.sealedBody(ctx, universeID, wr)
	if err != nil {
		return Secret{}, err
	}
	body.ID = wr.Name
	var got secretWire
	if _, err := sc.c.Do(ctx, http.MethodPost, secretsBase(universeID), body, &got); err != nil {
		return Secret{}, err
	}
	return got.toDomain(), nil
}

func (sc *secretClient) Update(ctx context.Context, universeID string, wr SecretWrite) (Secret, error) {
	body, err := sc.sealedBody(ctx, universeID, wr)
	if err != nil {
		return Secret{}, err
	}
	var got secretWire
	path := secretsBase(universeID) + "/" + url.PathEscape(wr.Name)
	if _, err := sc.c.Do(ctx, http.MethodPatch, path, body, &got); err != nil {
		return Secret{}, err
	}
	return got.toDomain(), nil
}

// sealedBody fetches the public key and returns a wire body carrying the sealed
// ciphertext, key id, and domain — never the plaintext.
func (sc *secretClient) sealedBody(ctx context.Context, universeID string, wr SecretWrite) (secretWire, error) {
	pub, keyID, err := sc.publicKey(ctx, universeID)
	if err != nil {
		return secretWire{}, err
	}
	cipher, err := seal(pub, wr.Value)
	if err != nil {
		return secretWire{}, err
	}
	return secretWire{Secret: cipher, KeyID: keyID, Domain: wr.Domain}, nil
}

func (sc *secretClient) Delete(ctx context.Context, universeID, name string) error {
	_, err := sc.c.Do(ctx, http.MethodDelete, secretsBase(universeID)+"/"+url.PathEscape(name), nil, nil)
	return err
}
