package auth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrForbidden       = errors.New("forbidden")
	ErrConflict        = errors.New("conflict")
	ErrNotFound        = errors.New("not found")
)

type User struct {
	ID           string
	Login        string
	Email        string
	Name         string
	PasswordHash string
	Role         Role
	Status       Status
}

type CreateUser struct {
	Login, Email, Name, Password string
	Role                         Role
}

type UpdateUser struct {
	Name, Email, Password *string
	Role                  *Role
	Status                *Status
}

type PermissionBundle struct {
	ID          string
	Name        string
	Permissions []Permission
}

type Repository struct{ db *database.Pool }

func NewRepository(pool *database.Pool) *Repository { return &Repository{db: pool} }

func (r *Repository) Create(ctx context.Context, input CreateUser) (User, error) {
	if !ValidRole(input.Role) || strings.TrimSpace(input.Login) == "" || strings.TrimSpace(input.Email) == "" || strings.TrimSpace(input.Name) == "" {
		return User{}, fmt.Errorf("invalid user")
	}
	hash, err := HashPassword(input.Password)
	if err != nil {
		return User{}, err
	}
	user := User{ID: uuid.NewString(), Login: strings.TrimSpace(input.Login), Email: strings.TrimSpace(input.Email), Name: strings.TrimSpace(input.Name), PasswordHash: hash, Role: input.Role, Status: Active}
	_, err = r.db.Exec(ctx, `INSERT INTO users (id, login, email, name, password_hash, role, status) VALUES ($1,$2,$3,$4,$5,$6,$7)`, user.ID, user.Login, user.Email, user.Name, user.PasswordHash, user.Role, user.Status)
	if err != nil {
		return User{}, normalizeWriteError(err)
	}
	return user, nil
}

func (r *Repository) ByLogin(ctx context.Context, login string) (User, error) {
	return r.by(ctx, `SELECT id, login, email, name, password_hash, role, status FROM users WHERE lower(login)=lower($1)`, login)
}

func (r *Repository) ByID(ctx context.Context, id string) (User, error) {
	return r.by(ctx, `SELECT id, login, email, name, password_hash, role, status FROM users WHERE id=$1`, id)
}

func (r *Repository) List(ctx context.Context) ([]User, error) {
	rows, err := r.db.Query(ctx, `SELECT id, login, email, name, password_hash, role, status FROM users ORDER BY lower(login), id`)
	if err != nil {
		return nil, fmt.Errorf("user list failed")
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.Login, &user.Email, &user.Name, &user.PasswordHash, &user.Role, &user.Status); err != nil {
			return nil, fmt.Errorf("user list failed")
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("user list failed")
	}
	return users, nil
}

func (r *Repository) Update(ctx context.Context, id string, input UpdateUser) (User, error) {
	var result User
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// All role/status transitions share this lock, so two concurrent updates
		// cannot each remove a different active administrator.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(10010)`); err != nil {
			return fmt.Errorf("administrator lock failed")
		}
		var current User
		if err := tx.QueryRow(ctx, `SELECT id, login, email, name, password_hash, role, status FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&current.ID, &current.Login, &current.Email, &current.Name, &current.PasswordHash, &current.Role, &current.Status); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("user lookup failed")
		}
		result = current
		if input.Name != nil && strings.TrimSpace(*input.Name) != "" {
			result.Name = strings.TrimSpace(*input.Name)
		} else if input.Name != nil {
			return fmt.Errorf("invalid user")
		}
		if input.Email != nil && strings.TrimSpace(*input.Email) != "" {
			result.Email = strings.TrimSpace(*input.Email)
		} else if input.Email != nil {
			return fmt.Errorf("invalid user")
		}
		if input.Role != nil {
			if !ValidRole(*input.Role) {
				return fmt.Errorf("invalid user")
			}
			result.Role = *input.Role
		}
		if input.Status != nil {
			if !ValidStatus(*input.Status) {
				return fmt.Errorf("invalid user")
			}
			result.Status = *input.Status
		}
		if current.Role == Administrator && current.Status == Active && (result.Role != Administrator || result.Status != Active) {
			var activeAdmins int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE role='administrator' AND status='active'`).Scan(&activeAdmins); err != nil {
				return fmt.Errorf("administrator check failed")
			}
			if activeAdmins <= 1 {
				return ErrForbidden
			}
		}
		if input.Password != nil {
			hash, err := HashPassword(*input.Password)
			if err != nil {
				return err
			}
			result.PasswordHash = hash
		}
		_, err := tx.Exec(ctx, `UPDATE users SET email=$2, name=$3, password_hash=$4, role=$5, status=$6, updated_at=now() WHERE id=$1`, result.ID, result.Email, result.Name, result.PasswordHash, result.Role, result.Status)
		return normalizeWriteError(err)
	})
	if err != nil {
		return User{}, err
	}
	return result, nil
}

func (r *Repository) by(ctx context.Context, query string, arg string) (User, error) {
	var user User
	err := r.db.QueryRow(ctx, query, arg).Scan(&user.ID, &user.Login, &user.Email, &user.Name, &user.PasswordHash, &user.Role, &user.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("user lookup failed")
	}
	return user, nil
}

func (r *Repository) CreateSession(ctx context.Context, userID string) (string, string, error) {
	session, token, csrf, err := NewSession(userID)
	if err != nil {
		return "", "", err
	}
	_, err = r.db.Exec(ctx, `INSERT INTO sessions(token_hash, csrf_hash, user_id, expires_at) VALUES ($1,$2,$3,$4)`, session.TokenHash, session.CSRFHash, userID, session.ExpiresAt)
	if err != nil {
		return "", "", fmt.Errorf("session creation failed")
	}
	return token, csrf, nil
}

func (r *Repository) PrincipalBySession(ctx context.Context, raw string) (Principal, string, error) {
	var principal Principal
	var csrfHash string
	err := r.db.QueryRow(ctx, `SELECT u.id, u.login, u.email, u.name, u.role, s.csrf_hash
FROM sessions s JOIN users u ON u.id=s.user_id
WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at > now() AND u.status='active'`, HashSecret(raw)).Scan(&principal.UserID, &principal.Login, &principal.Email, &principal.Name, &principal.Role, &csrfHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, "", ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, "", fmt.Errorf("session lookup failed")
	}
	return principal, csrfHash, nil
}

func (r *Repository) RevokeSession(ctx context.Context, raw string) error {
	_, err := r.db.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, HashSecret(raw))
	if err != nil {
		return fmt.Errorf("session revocation failed")
	}
	return nil
}

func normalizeWriteError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return fmt.Errorf("user write failed")
}

func (r *Repository) EffectivePermissions(ctx context.Context, principal Principal) ([]Permission, error) {
	set := map[Permission]struct{}{}
	for _, permission := range RolePermissions(principal.Role) {
		set[permission] = struct{}{}
	}
	rows, err := r.db.Query(ctx, `SELECT b.permissions FROM permission_bundles b JOIN user_permission_bundles ub ON ub.bundle_id=b.id WHERE ub.user_id=$1`, principal.UserID)
	if err != nil {
		return nil, fmt.Errorf("permission lookup failed")
	}
	defer rows.Close()
	for rows.Next() {
		var permissions []string
		if err := rows.Scan(&permissions); err != nil {
			return nil, fmt.Errorf("permission lookup failed")
		}
		for _, raw := range permissions {
			permission := Permission(raw)
			if ValidPermission(permission) {
				set[permission] = struct{}{}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("permission lookup failed")
	}
	result := make([]Permission, 0, len(set))
	for permission := range set {
		result = append(result, permission)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func (r *Repository) ListBundles(ctx context.Context) ([]PermissionBundle, error) {
	rows, err := r.db.Query(ctx, `SELECT id, name, permissions FROM permission_bundles ORDER BY lower(name), id`)
	if err != nil {
		return nil, fmt.Errorf("bundle list failed")
	}
	defer rows.Close()
	var result []PermissionBundle
	for rows.Next() {
		var bundle PermissionBundle
		var raw []string
		if err := rows.Scan(&bundle.ID, &bundle.Name, &raw); err != nil {
			return nil, fmt.Errorf("bundle list failed")
		}
		for _, value := range raw {
			bundle.Permissions = append(bundle.Permissions, Permission(value))
		}
		result = append(result, bundle)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("bundle list failed")
	}
	return result, nil
}

func (r *Repository) CreateBundle(ctx context.Context, name string, permissions []Permission) (PermissionBundle, error) {
	if strings.TrimSpace(name) == "" {
		return PermissionBundle{}, fmt.Errorf("invalid bundle")
	}
	seen := map[Permission]struct{}{}
	raw := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		if !ValidPermission(permission) || permission == PermissionRoleManage {
			return PermissionBundle{}, fmt.Errorf("invalid bundle permission")
		}
		if _, exists := seen[permission]; !exists {
			seen[permission] = struct{}{}
			raw = append(raw, string(permission))
		}
	}
	sort.Strings(raw)
	bundle := PermissionBundle{ID: uuid.NewString(), Name: strings.TrimSpace(name)}
	for _, permission := range raw {
		bundle.Permissions = append(bundle.Permissions, Permission(permission))
	}
	_, err := r.db.Exec(ctx, `INSERT INTO permission_bundles(id, name, permissions) VALUES ($1,$2,$3)`, bundle.ID, bundle.Name, raw)
	if err != nil {
		return PermissionBundle{}, normalizeWriteError(err)
	}
	return bundle, nil
}

func (r *Repository) SetUserBundles(ctx context.Context, userID string, bundleIDs []string) error {
	return r.db.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, userID).Scan(&exists); err != nil || !exists {
			if err != nil {
				return fmt.Errorf("user lookup failed")
			}
			return ErrNotFound
		}
		unique := map[string]struct{}{}
		for _, id := range bundleIDs {
			if _, duplicate := unique[id]; duplicate {
				continue
			}
			unique[id] = struct{}{}
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM permission_bundles WHERE id=$1)`, id).Scan(&exists); err != nil || !exists {
				if err != nil {
					return fmt.Errorf("bundle lookup failed")
				}
				return ErrNotFound
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_permission_bundles WHERE user_id=$1`, userID); err != nil {
			return fmt.Errorf("bundle assignment failed")
		}
		for id := range unique {
			if _, err := tx.Exec(ctx, `INSERT INTO user_permission_bundles(user_id,bundle_id) VALUES($1,$2)`, userID, id); err != nil {
				return fmt.Errorf("bundle assignment failed")
			}
		}
		return nil
	})
}
