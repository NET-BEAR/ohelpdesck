package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrNotFound                      = errors.New("channel not found")
	ErrInvalid                       = errors.New("invalid channel")
	ErrCredentialsMissing            = errors.New("channel credentials missing")
	ErrProviderValidationUnavailable = errors.New("provider validation unavailable")
	ErrStaleConfig                   = errors.New("channel configuration changed during validation")
)

type Channel struct {
	ID             string          `json:"id"`
	Type           Type            `json:"type"`
	Name           string          `json:"name"`
	Status         Status          `json:"status"`
	Enabled        bool            `json:"enabled"`
	Config         json.RawMessage `json:"config"`
	HasCredentials bool            `json:"has_credentials"`
	ConfigVersion  int64           `json:"-"`
}

type CreateInput struct {
	Type        Type            `json:"type"`
	Name        string          `json:"name"`
	Config      json.RawMessage `json:"config"`
	Credentials json.RawMessage `json:"credentials"`
}

type PatchInput struct {
	Name        *string          `json:"name,omitempty"`
	Config      *json.RawMessage `json:"config,omitempty"`
	Credentials *json.RawMessage `json:"credentials,omitempty"`
}

type Validation struct {
	Valid bool `json:"valid"`
}
type AuditContext struct{ ActorID, CorrelationID string }

type Repository struct{ db *database.Pool }

func NewRepository(db *database.Pool) *Repository { return &Repository{db: db} }

// Service centralizes state transitions and only passes encrypted credentials
// into Repository. The repository deliberately has no API returning ciphertext.
type Service struct {
	repository *Repository
	keyring    *Keyring
	registry   *Registry
}

func NewService(db *database.Pool, keyring *Keyring) *Service {
	return &Service{repository: NewRepository(db), keyring: keyring, registry: NewRegistry()}
}
func NewServiceWithRegistry(db *database.Pool, keyring *Keyring, registry *Registry) *Service {
	return &Service{repository: NewRepository(db), keyring: keyring, registry: registry}
}

func (s *Service) List(ctx context.Context) ([]Channel, error) { return s.repository.List(ctx) }
func (s *Service) Get(ctx context.Context, id string) (Channel, error) {
	return s.repository.Get(ctx, id)
}

func (s *Service) Create(ctx context.Context, audit AuditContext, input CreateInput) (Channel, error) {
	if err := validateCreate(input); err != nil {
		return Channel{}, err
	}
	id := uuid.NewString()
	sealed, metadata, err := s.seal(id, input.Type, input.Credentials)
	if err != nil {
		return Channel{}, err
	}
	return s.repository.Create(ctx, audit, id, input, sealed, metadata)
}

func (s *Service) Patch(ctx context.Context, audit AuditContext, id string, input PatchInput) (Channel, error) {
	if input.Name == nil && input.Config == nil && input.Credentials == nil {
		return Channel{}, ErrInvalid
	}
	if input.Name != nil && strings.TrimSpace(*input.Name) == "" {
		return Channel{}, ErrInvalid
	}
	if input.Config != nil && !validJSONObject(*input.Config) {
		return Channel{}, ErrInvalid
	}
	current, lookupErr := s.repository.Get(ctx, id)
	if lookupErr != nil {
		return Channel{}, lookupErr
	}
	var sealed []byte
	var metadata CredentialMetadata
	var err error
	if input.Credentials != nil {
		if !validJSONObject(*input.Credentials) {
			return Channel{}, ErrInvalid
		}
		sealed, metadata, err = s.seal(id, current.Type, *input.Credentials)
		if err != nil {
			return Channel{}, err
		}
	}
	return s.repository.Patch(ctx, audit, id, current.ConfigVersion, input, sealed, metadata)
}

// Validate performs only local, side-effect-free validation. Provider network
// calls belong to their respective adapter and are intentionally not made by
// the admin API.
func (s *Service) Validate(ctx context.Context, audit AuditContext, id string) (Validation, error) {
	channel, err := s.repository.Get(ctx, id)
	if err != nil {
		return Validation{}, err
	}
	result := Validation{Valid: s.registry.ValidateForEnable(ctx, channel) == nil}
	if err := s.repository.Audit(ctx, audit, id, "validated"); err != nil {
		return Validation{}, err
	}
	return result, nil
}

func (s *Service) Enable(ctx context.Context, audit AuditContext, id string) (Channel, error) {
	channel, err := s.repository.Get(ctx, id)
	if err != nil {
		return Channel{}, err
	}
	if err := s.registry.ValidateForEnable(ctx, channel); err != nil {
		return Channel{}, err
	}
	return s.repository.SetEnabledIfVersion(ctx, audit, id, channel.ConfigVersion, true, "enabled")
}

func (s *Service) Disable(ctx context.Context, audit AuditContext, id string) (Channel, error) {
	return s.repository.SetEnabled(ctx, audit, id, false, "disabled")
}

// ReencryptCredentials is an explicit, controlled key-rotation operation.
func (s *Service) ReencryptCredentials(ctx context.Context, audit AuditContext, id string) (Channel, error) {
	channel, sealed, metadata, err := s.repository.credentials(ctx, id)
	if err != nil {
		return Channel{}, err
	}
	plain, err := s.keyring.Open(channel.ID, channel.Type, metadata, sealed)
	if err != nil {
		return Channel{}, err
	}
	next, nextMeta, err := s.keyring.Seal(channel.ID, channel.Type, plain)
	if err != nil {
		return Channel{}, err
	}
	raw := json.RawMessage(plain)
	input := PatchInput{Credentials: &raw}
	return s.repository.Patch(ctx, audit, id, channel.ConfigVersion, input, next, nextMeta)
}

// Credentials returns decrypted bytes only to an in-process adapter boundary.
func (s *Service) Credentials(ctx context.Context, id string) ([]byte, error) {
	c, sealed, meta, err := s.repository.credentials(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.keyring.Open(c.ID, c.Type, meta, sealed)
}

// Audit records only action metadata; configuration and credentials are never
// included in durable audit details.

func (s *Service) seal(id string, channelType Type, raw json.RawMessage) ([]byte, CredentialMetadata, error) {
	if s == nil || s.keyring == nil {
		return nil, CredentialMetadata{}, fmt.Errorf("channel credential encryption is unavailable")
	}
	return s.keyring.Seal(id, channelType, raw)
}

func validateCreate(input CreateInput) error {
	if !ValidType(input.Type) || strings.TrimSpace(input.Name) == "" || !validJSONObject(input.Config) || !validJSONObject(input.Credentials) {
		return ErrInvalid
	}
	return nil
}

func validJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func (r *Repository) List(ctx context.Context) ([]Channel, error) {
	rows, err := r.db.Query(ctx, `SELECT id,type,name,status,enabled,config,credentials_ciphertext IS NOT NULL,config_version FROM channels ORDER BY lower(name),id`)
	if err != nil {
		return nil, fmt.Errorf("channel list failed")
	}
	defer rows.Close()
	var result []Channel
	for rows.Next() {
		var channel Channel
		if err := rows.Scan(&channel.ID, &channel.Type, &channel.Name, &channel.Status, &channel.Enabled, &channel.Config, &channel.HasCredentials, &channel.ConfigVersion); err != nil {
			return nil, fmt.Errorf("channel list failed")
		}
		result = append(result, channel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channel list failed")
	}
	return result, nil
}

func (r *Repository) Get(ctx context.Context, id string) (Channel, error) {
	var channel Channel
	err := r.db.QueryRow(ctx, `SELECT id,type,name,status,enabled,config,credentials_ciphertext IS NOT NULL,config_version FROM channels WHERE id=$1`, id).Scan(&channel.ID, &channel.Type, &channel.Name, &channel.Status, &channel.Enabled, &channel.Config, &channel.HasCredentials, &channel.ConfigVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Channel{}, ErrNotFound
	}
	if err != nil {
		return Channel{}, fmt.Errorf("channel lookup failed")
	}
	return channel, nil
}

func (r *Repository) Create(ctx context.Context, audit AuditContext, id string, input CreateInput, sealed []byte, metadata CredentialMetadata) (Channel, error) {
	channel := Channel{ID: id, Type: input.Type, Name: strings.TrimSpace(input.Name), Status: StatusDisabled, Enabled: false, Config: input.Config, HasCredentials: true}
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO channels(id,type,name,status,enabled,config,credentials_ciphertext,credentials_key_id,credentials_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, channel.ID, channel.Type, channel.Name, channel.Status, channel.Enabled, channel.Config, sealed, metadata.KeyID, metadata.Version)
		if err != nil {
			return err
		}
		return appendAudit(ctx, tx, audit, channel.ID, "created")
	})
	if err != nil {
		return Channel{}, fmt.Errorf("channel create failed")
	}
	return channel, nil
}

func (r *Repository) Patch(ctx context.Context, audit AuditContext, id string, expectedVersion int64, input PatchInput, sealed []byte, metadata CredentialMetadata) (Channel, error) {
	var name any
	if input.Name != nil {
		name = *input.Name
	}
	var config any
	if input.Config != nil {
		config = *input.Config
	}
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		command, err := tx.Exec(ctx, `UPDATE channels SET name=COALESCE($2,name), config=COALESCE($3,config), config_version=config_version+CASE WHEN $3 IS NULL AND NOT $4::boolean THEN 0 ELSE 1 END, credentials_ciphertext=CASE WHEN $4::boolean THEN $5 ELSE credentials_ciphertext END, credentials_key_id=CASE WHEN $4::boolean THEN $6 ELSE credentials_key_id END, credentials_version=CASE WHEN $4::boolean THEN $7 ELSE credentials_version END, updated_at=now() WHERE id=$1 AND config_version=$8`, id, name, config, input.Credentials != nil, sealed, metadata.KeyID, metadata.Version, expectedVersion)
		if err != nil {
			return err
		}
		if command.RowsAffected() == 0 {
			return ErrStaleConfig
		}
		return appendAudit(ctx, tx, audit, id, "patched")
	})
	if err != nil {
		return Channel{}, err
	}
	return r.Get(ctx, id)
}

func (r *Repository) SetEnabled(ctx context.Context, audit AuditContext, id string, enabled bool, action string) (Channel, error) {
	return r.SetEnabledIfVersion(ctx, audit, id, 0, enabled, action)
}
func (r *Repository) SetEnabledIfVersion(ctx context.Context, audit AuditContext, id string, expectedVersion int64, enabled bool, action string) (Channel, error) {
	status := StatusDisabled
	if enabled {
		status = StatusActive
	}
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		query := `UPDATE channels SET enabled=$2,status=$3,updated_at=now() WHERE id=$1`
		args := []any{id, enabled, status}
		if expectedVersion > 0 {
			query += ` AND config_version=$4`
			args = append(args, expectedVersion)
		}
		command, err := tx.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		if command.RowsAffected() == 0 {
			if expectedVersion > 0 {
				return ErrStaleConfig
			}
			return ErrNotFound
		}
		return appendAudit(ctx, tx, audit, id, action)
	})
	if err != nil {
		return Channel{}, err
	}
	return r.Get(ctx, id)
}

func (r *Repository) Audit(ctx context.Context, audit AuditContext, channelID, action string) error {
	return r.db.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error { return appendAudit(ctx, tx, audit, channelID, action) })
}
func (r *Repository) credentials(ctx context.Context, id string) (Channel, []byte, CredentialMetadata, error) {
	var c Channel
	var sealed []byte
	var m CredentialMetadata
	err := r.db.QueryRow(ctx, `SELECT id,type,name,status,enabled,config,credentials_ciphertext IS NOT NULL,config_version,credentials_ciphertext,credentials_key_id,credentials_version FROM channels WHERE id=$1`, id).Scan(&c.ID, &c.Type, &c.Name, &c.Status, &c.Enabled, &c.Config, &c.HasCredentials, &c.ConfigVersion, &sealed, &m.KeyID, &m.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Channel{}, nil, CredentialMetadata{}, ErrNotFound
	}
	if err != nil {
		return Channel{}, nil, CredentialMetadata{}, fmt.Errorf("channel credential lookup failed")
	}
	return c, sealed, m, nil
}
func appendAudit(ctx context.Context, tx pgx.Tx, audit AuditContext, channelID, action string) error {
	actor, err := uuid.Parse(audit.ActorID)
	if err != nil {
		return fmt.Errorf("invalid audit actor")
	}
	correlation, err := uuid.Parse(audit.CorrelationID)
	if err != nil {
		return fmt.Errorf("invalid audit correlation")
	}
	_, err = tx.Exec(ctx, `INSERT INTO channel_audit_events(id,channel_id,actor_id,correlation_id,action) VALUES($1,$2,$3,$4,$5)`, uuid.New(), channelID, actor, correlation, action)
	if err != nil {
		return fmt.Errorf("channel audit failed")
	}
	return nil
}
