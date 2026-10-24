package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mariner/internal/auth"
	"mariner/internal/model"
)

type fakeConnectionUseCase struct {
	connections []model.Connection
	created     model.Connection
}

func (f *fakeConnectionUseCase) List(string, string) ([]model.Connection, error) {
	return f.connections, nil
}

func (f *fakeConnectionUseCase) Create(_ context.Context, _, _ string, _ []string, connection model.Connection) (string, error) {
	f.created = connection
	return "connection-1", nil
}

func (f *fakeConnectionUseCase) Test(context.Context, string, string, []string, model.Connection, bool) error {
	return nil
}

func (f *fakeConnectionUseCase) Update(context.Context, string, string, []string, model.Connection) error {
	return nil
}

func (f *fakeConnectionUseCase) Delete(string, string, []string, string) error { return nil }

func testSession(*http.Request) (auth.Session, error) {
	return auth.Session{User: auth.User{ID: "user-1"}, Password: "password"}, nil
}

func TestConnectionControllerListRedactsSecrets(t *testing.T) {
	api := &fakeConnectionUseCase{connections: []model.Connection{{ID: "c1", Name: "primary", Bucket: "bucket", AccessKey: "access", SecretKey: "secret"}}}
	controller := &ConnectionController{API: api, Unlocked: testSession}
	recorder := httptest.NewRecorder()
	controller.List(recorder, httptest.NewRequest(http.MethodGet, "/api/connections", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "access") || strings.Contains(body, "secret") {
		t.Fatalf("response leaked credentials: %s", body)
	}
	var response []map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response[0]["id"] != "c1" {
		t.Fatalf("response = %#v", response)
	}
}

func TestConnectionControllerCreateMapsRequest(t *testing.T) {
	api := &fakeConnectionUseCase{}
	controller := &ConnectionController{API: api, Unlocked: testSession}
	recorder := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"primary","bucket":"bucket","accessKey":"access","secretKey":"secret"}`)
	controller.Create(recorder, httptest.NewRequest(http.MethodPost, "/api/connections", body))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if api.created.Name != "primary" || api.created.Bucket != "bucket" || api.created.SecretKey != "secret" {
		t.Fatalf("created connection = %#v", api.created)
	}
}

func TestConnectionControllerRequiresUnlockedSession(t *testing.T) {
	controller := &ConnectionController{API: &fakeConnectionUseCase{}, Unlocked: func(*http.Request) (auth.Session, error) {
		return auth.Session{}, errors.New("vault is locked")
	}}
	recorder := httptest.NewRecorder()
	controller.List(recorder, httptest.NewRequest(http.MethodGet, "/api/connections", nil))
	if recorder.Code != 423 {
		t.Fatalf("status = %d, want 423", recorder.Code)
	}
}
