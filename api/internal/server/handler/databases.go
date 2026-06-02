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
	Add(ctx context.Context, app store.App, engine string) (store.ManagedDatabase, error)
	Remove(ctx context.Context, appID, id string) error
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
		m, err := prov.Add(r.Context(), app, req.Engine)
		if err != nil {
			switch {
			case errors.Is(err, dbprovision.ErrUnsupportedEngine):
				WriteError(w, http.StatusUnprocessableEntity, "unsupported engine")
			case errors.Is(err, dbprovision.ErrEncryptionDisabled):
				WriteError(w, http.StatusUnprocessableEntity, "encryption is disabled (VAC_MASTER_KEY unset); managed databases need it")
			case errors.Is(err, store.ErrConflict):
				WriteError(w, http.StatusConflict, "a database of this kind already exists for this app")
			default:
				WriteError(w, http.StatusInternalServerError, "could not provision database")
			}
			return
		}
		audit.SetTarget(r.Context(), "managed_db", m.ID)
		audit.Describe(r.Context(), "provisioned "+req.Engine+" for "+app.Slug)
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
		audit.Describe(r.Context(), "removed managed database")
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
