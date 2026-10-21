package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"mariner/internal/api"
	"mariner/internal/auth"
	"mariner/internal/httpapi/dto"
	"mariner/internal/httpapi/mapper"
	"mariner/internal/model"
)

type ConnectionUseCase interface {
	List(userID, password string) ([]model.Connection, error)
	Create(ctx context.Context, userID, password string, connection model.Connection) (string, error)
	Test(ctx context.Context, userID, password string, connection model.Connection, admin bool) error
	Update(ctx context.Context, userID, password string, connection model.Connection) error
	Delete(userID, password, id string) error
}

type ConnectionController struct {
	API              ConnectionUseCase
	Unlocked         func(*http.Request) (auth.Session, error)
	AdminAllowed     func(*http.Request) (auth.Session, bool)
	MergeConnections func(auth.User, []model.Connection) []model.Connection
	Audit            func(auth.Session, string, string, map[string]string)
}

func (c *ConnectionController) List(w http.ResponseWriter, r *http.Request) {
	session, err := c.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	connections, err := c.API.List(session.User.ID, session.Password)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	if c.MergeConnections != nil {
		connections = c.MergeConnections(session.User, connections)
	}
	responses := make([]dto.ConnectionResponse, 0, len(connections))
	for _, connection := range connections {
		responses = append(responses, mapper.ConnectionToResponse(connection))
	}
	write(w, responses)
}

func (c *ConnectionController) Create(w http.ResponseWriter, r *http.Request) {
	session, err := c.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	connection, ok := decodeConnection(w, r)
	if !ok {
		return
	}
	id, err := c.API.Create(r.Context(), session.User.ID, session.Password, connection)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	write(w, mapper.ConnectionID(id))
	c.audit(session, "connection.create", "success", map[string]string{"connection_id": id, "bucket": connection.Bucket})
}

func (c *ConnectionController) Test(w http.ResponseWriter, r *http.Request) {
	connection, ok := decodeConnection(w, r)
	if !ok {
		return
	}
	session, admin := c.AdminAllowed(r)
	if !admin {
		var err error
		session, err = c.unlocked(r)
		if err != nil {
			fail(w, 423, err)
			return
		}
	}
	if err := c.API.Test(r.Context(), session.User.ID, session.Password, connection, admin); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	write(w, map[string]bool{"ok": true})
}

func (c *ConnectionController) Update(w http.ResponseWriter, r *http.Request) {
	session, err := c.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	connection, ok := decodeConnection(w, r)
	if !ok {
		return
	}
	if connection.ID == "" {
		fail(w, http.StatusBadRequest, errors.New("connection id is required"))
		return
	}
	if err := c.API.Update(r.Context(), session.User.ID, session.Password, connection); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, api.ErrOrganizationConnection) {
			status = http.StatusForbidden
		} else if errors.Is(err, api.ErrConnectionNotFound) {
			status = http.StatusNotFound
		}
		fail(w, status, err)
		return
	}
	write(w, map[string]bool{"ok": true})
	c.audit(session, "connection.update", "success", map[string]string{"connection_id": connection.ID, "bucket": connection.Bucket})
}

func (c *ConnectionController) Delete(w http.ResponseWriter, r *http.Request) {
	session, err := c.unlocked(r)
	if err != nil {
		fail(w, 423, err)
		return
	}
	id := r.URL.Query().Get("id")
	if err := c.API.Delete(session.User.ID, session.Password, id); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, api.ErrOrganizationConnection) {
			status = http.StatusForbidden
		}
		fail(w, status, err)
		return
	}
	write(w, map[string]bool{"ok": true})
	c.audit(session, "connection.delete", "success", map[string]string{"connection_id": id})
}

func decodeConnection(w http.ResponseWriter, r *http.Request) (model.Connection, bool) {
	var input dto.ConnectionRequest
	if decodeJSON(w, r, &input) != nil {
		fail(w, http.StatusBadRequest, errors.New("invalid connection request"))
		return model.Connection{}, false
	}
	connection, err := mapper.ConnectionFromRequest(input)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return model.Connection{}, false
	}
	return connection, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("request contains multiple JSON values")
		}
		return err
	}
	return nil
}

func (c *ConnectionController) unlocked(r *http.Request) (auth.Session, error) {
	if c.API == nil || c.Unlocked == nil {
		return auth.Session{}, errors.New("connection controller is not configured")
	}
	return c.Unlocked(r)
}

func (c *ConnectionController) audit(session auth.Session, action, result string, fields map[string]string) {
	if c.Audit != nil {
		c.Audit(session, action, result, fields)
	}
}

func write(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, err error) {
	if status >= http.StatusInternalServerError {
		err = errors.New("request could not be completed")
	}
	http.Error(w, err.Error(), status)
}
