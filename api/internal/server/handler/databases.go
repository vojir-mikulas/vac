package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/dbprovision"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// DBProvisioner is the managed-database surface the handlers depend on.
// *dbprovision.Provisioner satisfies it.
type DBProvisioner interface {
	AvailableEngines() []dbprovision.EngineInfo
	EngineInfoFor(name string) (dbprovision.EngineInfo, bool)
	Add(ctx context.Context, app store.App, engine, envVarName string) (store.ManagedDatabase, error)
	Remove(ctx context.Context, appID, id string) error
	DatabaseInventory(ctx context.Context) (dbprovision.Inventory, error)
}

type managedDatabaseDTO struct {
	ID          string    `json:"id"`
	AppID       string    `json:"app_id"`
	Engine      string    `json:"engine"`
	DBName      string    `json:"db_name"`
	RoleName    *string   `json:"role_name,omitempty"`
	EnvVarName  string    `json:"env_var_name"`
	Status      string    `json:"status"`
	Error       *string   `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	FootprintMB int       `json:"footprint_mb"`
	Shared      bool      `json:"shared"`
}

func toManagedDatabaseDTO(m store.ManagedDatabase, info dbprovision.EngineInfo) managedDatabaseDTO {
	return managedDatabaseDTO{
		ID:          m.ID,
		AppID:       m.AppID,
		Engine:      m.Engine,
		DBName:      m.DBName,
		RoleName:    m.RoleName,
		EnvVarName:  m.EnvVarName,
		Status:      m.Status,
		Error:       m.Error,
		CreatedAt:   m.CreatedAt,
		FootprintMB: info.FootprintMB,
		Shared:      info.Shared,
	}
}

// --- box-wide inventory (plan 20) ---

type dbBackupSummaryDTO struct {
	Status     string     `json:"status"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	SizeBytes  *int64     `json:"size_bytes,omitempty"`
}

type dbInventoryEntryDTO struct {
	ID             string              `json:"id,omitempty"`
	AppID          string              `json:"app_id,omitempty"`
	AppSlug        string              `json:"app_slug,omitempty"`
	AppName        string              `json:"app_name,omitempty"`
	DBName         string              `json:"db_name"`
	EnvVarName     string              `json:"env_var_name,omitempty"`
	Status         string              `json:"status"`
	SizeBytes      *int64              `json:"size_bytes"`
	LastBackup     *dbBackupSummaryDTO `json:"last_backup,omitempty"`
	IsControlPlane bool                `json:"is_control_plane"`
}

type dbEngineGroupDTO struct {
	Engine      string                `json:"engine"`
	FootprintMB int                   `json:"footprint_mb"`
	Shared      bool                  `json:"shared"`
	Databases   []dbInventoryEntryDTO `json:"databases"`
}

type dbInventoryDTO struct {
	Engines []dbEngineGroupDTO `json:"engines"`
}

func toInventoryDTO(inv dbprovision.Inventory) dbInventoryDTO {
	out := dbInventoryDTO{Engines: make([]dbEngineGroupDTO, 0, len(inv.Engines))}
	for _, g := range inv.Engines {
		group := dbEngineGroupDTO{
			Engine:      g.Engine,
			FootprintMB: g.FootprintMB,
			Shared:      g.Shared,
			Databases:   make([]dbInventoryEntryDTO, 0, len(g.Databases)),
		}
		for _, d := range g.Databases {
			entry := dbInventoryEntryDTO{
				ID:             d.ID,
				AppID:          d.AppID,
				AppSlug:        d.AppSlug,
				AppName:        d.AppName,
				DBName:         d.DBName,
				EnvVarName:     d.EnvVarName,
				Status:         d.Status,
				SizeBytes:      d.SizeBytes,
				IsControlPlane: d.IsControlPlane,
			}
			if d.LastBackup != nil {
				entry.LastBackup = &dbBackupSummaryDTO{
					Status:     d.LastBackup.Status,
					FinishedAt: d.LastBackup.FinishedAt,
					SizeBytes:  d.LastBackup.SizeBytes,
				}
			}
			group.Databases = append(group.Databases, entry)
		}
		out.Engines = append(out.Engines, group)
	}
	return out
}

// DatabaseInventory returns the box-wide, per-engine database inventory for the
// Database section. Gated by VAC_MANAGED_SERVICES at the route layer.
func DatabaseInventory(prov DBProvisioner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inv, err := prov.DatabaseInventory(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load database inventory")
			return
		}
		WriteJSON(w, http.StatusOK, toInventoryDTO(inv))
	}
}

// ListDatabaseEngines returns the engine catalog (name + footprint) for the UI
// picker.
func ListDatabaseEngines(prov DBProvisioner) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, prov.AvailableEngines())
	}
}

// ListDatabases returns an app's managed databases.
func ListDatabases(s *store.Store, prov DBProvisioner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		dbs, err := s.ListManagedDatabasesForApp(r.Context(), appID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not list databases")
			return
		}
		out := make([]managedDatabaseDTO, 0, len(dbs))
		for _, m := range dbs {
			info, _ := prov.EngineInfoFor(m.Engine)
			out = append(out, toManagedDatabaseDTO(m, info))
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

type addDatabaseReq struct {
	Engine string `json:"engine"`
	// EnvVarName is the env var the connection string is injected as. Empty lets
	// the backend pick a unique default (DATABASE_URL, then a suffixed name when
	// the app already has a DB bound to DATABASE_URL).
	EnvVarName string `json:"env_var_name"`
}

type addDatabaseResp struct {
	Database managedDatabaseDTO `json:"database"`
	Warning  string             `json:"warning,omitempty"`
}

// AddDatabase provisions a managed database of the requested engine. Returns 202
// — provisioning (which may cold-start a shared daemon) runs in the background;
// the UI polls the row's status. The response carries the footprint warning.
func AddDatabase(s *store.Store, prov DBProvisioner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		app, err := s.GetApp(r.Context(), appID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load app")
			return
		}
		var req addDatabaseReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		info, ok := prov.EngineInfoFor(req.Engine)
		if !ok {
			WriteError(w, http.StatusUnprocessableEntity, "unsupported engine")
			return
		}
		m, err := prov.Add(r.Context(), app, req.Engine, req.EnvVarName)
		if err != nil {
			switch {
			case errors.Is(err, dbprovision.ErrUnsupportedEngine):
				WriteError(w, http.StatusUnprocessableEntity, "unsupported engine")
			case errors.Is(err, dbprovision.ErrEncryptionDisabled):
				WriteError(w, http.StatusUnprocessableEntity, "encryption is disabled (VAC_MASTER_KEY unset); managed databases need it")
			case errors.Is(err, dbprovision.ErrInvalidBindingName):
				WriteError(w, http.StatusBadRequest, "env_var_name must be an uppercase identifier (e.g. ANALYTICS_DATABASE_URL)")
			case errors.Is(err, store.ErrConflict):
				WriteError(w, http.StatusConflict, "that env var binding is already used by another database on this app; choose a different name")
			default:
				WriteError(w, http.StatusInternalServerError, "could not provision database")
			}
			return
		}
		audit.SetTarget(r.Context(), "managed_db", m.ID)
		audit.Action(r.Context(), "database.provisioned", map[string]any{"engine": req.Engine, "app": app.Slug})
		WriteJSON(w, http.StatusAccepted, addDatabaseResp{
			Database: toManagedDatabaseDTO(m, info),
			Warning:  footprintWarning(info),
		})
	}
}

// RemoveDatabase deprovisions a managed database (engine-side drop + env var +
// backup config + row).
func RemoveDatabase(s *store.Store, prov DBProvisioner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appID := chi.URLParam(r, "id")
		dbid := chi.URLParam(r, "dbid")
		if err := prov.Remove(r.Context(), appID, dbid); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "database not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not remove database")
			return
		}
		audit.SetTarget(r.Context(), "managed_db", dbid)
		audit.Action(r.Context(), "database.removed", nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

// footprintWarning renders the add-time RAM warning for a shared engine; Postgres
// (which shares vac-db) returns no warning.
func footprintWarning(info dbprovision.EngineInfo) string {
	if !info.Shared {
		return ""
	}
	return fmt.Sprintf("Starts a shared %s instance (~%d MB) the first time it's used.", info.Name, info.FootprintMB)
}
