package tests

import (
	"testing"

	"webtyp.com/fmt"
	"webtyp.com/json"
	"webtyp.com/model"
	"webtyp.com/orm"
	"webtyp.com/router/mock"
	"webtyp.com/storage/mem"
	businesshours "github.com/veltylabs/business_hours"
)

type fakeIDs struct{ n int }

func (f *fakeIDs) NewID() string {
	f.n++
	return "test-id-" + fmt.Convert(f.n).String() // webtyp.com/fmt — nunca stdlib strconv
}

// setup construye un Module sobre storage/mem y devuelve el MISMO manejador *orm.DB que recibió —
// seedWeek a continuación lo usa directamente (orm.DB.Create está exportado; no hay necesidad de acceder al
// campo db no exportado de Module, y todavía no hay una operación de escritura para sembrar datos a través de ella — ver
// las notas específicas de dominio en AGENTS.md sobre por qué la única operación de este módulo es de solo lectura).
func setup(t *testing.T) (*businesshours.Module, *orm.DB, *fakeIDs) {
	t.Helper()
	db := orm.New(mem.New())
	ids := &fakeIDs{}
	m, err := businesshours.New(db, businesshours.Deps{IDs: ids})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m, db, ids
}

// seedWeek inserta 7 filas a través del mismo *orm.DB que la prueba ya posee — la aplicación de composición
// raíz sembraría los datos de la misma manera, ya que este módulo no tiene una operación de escritura (ver AGENTS.md).
func seedWeek(t *testing.T, db *orm.DB, ids *fakeIDs) {
	t.Helper()
	for day := 0; day < 7; day++ {
		bh := &businesshours.BusinessHours{
			Id:        ids.NewID(),
			DayOfWeek: int64(day),
			OpenTime:  "08:00",
			CloseTime: "18:00",
			IsOpen:    day != 0 && day != 6,
			UpdatedAt: 123456789,
		}
		if !bh.IsOpen {
			bh.OpenTime, bh.CloseTime = "", ""
			bh.Notes = "Cerrado"
		}
		if err := db.Create(bh); err != nil {
			t.Fatalf("seed day %d: %v", day, err)
		}
	}
}

func TestGetSchedule_FullWeek(t *testing.T) {
	m, db, ids := setup(t)
	seedWeek(t, db, ids)

	rows, err := m.GetSchedule()
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if len(rows) != 7 {
		t.Fatalf("expected 7 rows, got %d", len(rows))
	}
	if !rows[1].IsOpen || rows[1].OpenTime != "08:00" || rows[1].CloseTime != "18:00" {
		t.Errorf("Monday incorrect: %+v", rows[1])
	}
	if rows[0].IsOpen || rows[0].Notes == "" {
		t.Errorf("Sunday incorrect: %+v", rows[0])
	}
}

func TestNew_RequiresIDs(t *testing.T) {
	db := orm.New(mem.New())
	if _, err := businesshours.New(db, businesshours.Deps{}); err == nil {
		t.Fatal("expected an error when Deps.IDs is nil")
	}
}

func TestGetSchedule_Empty(t *testing.T) {
	m, _, _ := setup(t)
	_, err := m.GetSchedule()
	if err != businesshours.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMountOperations_GetBusinessHours(t *testing.T) {
	m, db, ids := setup(t)
	seedWeek(t, db, ids)

	if m.ModelName() != "business_hours" {
		t.Fatalf("expected ModelName %q, got %q", "business_hours", m.ModelName())
	}

	// El valor cero de mock.Router es legal (sin Authn, sin Authorize) pero DENIEGA cada ruta
	// protegida (model.Allowed(nil, ...) == false) — configure un Authorize que lo permita, para que la
	// ruta de éxito realmente se ejercite, en lugar de pasar accidentalmente a través de un 403 que nadie verificó.
	reg := &mock.Router{}
	reg.Configure(mock.Config{
		Authorize: func(userID string, r model.Resource, a model.Action) bool { return true },
	})
	m.MountOperations(reg)

	routes := reg.Routes()
	if len(routes) != 1 {
		t.Fatalf("expected 1 registered op, got %d", len(routes))
	}
	info := routes[0]
	wantPath := "/" + businesshours.OpGetBusinessHours
	if info.Path != wantPath {
		t.Errorf("expected op path %q, got %q", wantPath, info.Path)
	}
	if info.Resource != "business_hours" || info.Action != model.Read {
		t.Errorf("expected Requires(business_hours, Read), got %q/%v", info.Resource, info.Action)
	}

	ctx := &mock.Context{}      // no se necesita cuerpo — la operación declara Accepts(nil) y nunca decodifica
	ctx.SetUserID("u1")         // AccessGuarded necesita una identidad antes de que se ejecute la puerta
	reg.Invoke("OP", wantPath, ctx)

	if ctx.Status != 0 {
		t.Fatalf("expected no error status, got %d; body=%s", ctx.Status, ctx.ResponseBody())
	}
	if len(ctx.ResponseBody()) == 0 {
		t.Error("expected a non-empty encoded response body")
	}

	// Decodifica la respuesta a través del codec real (no solo revisa que el body no esté vacío) —
	// prueba que el contrato de wire realmente funciona de punta a punta, y ejercita
	// BusinessHoursList.Append/DecodeFields, que ningún otro test recorre hoy.
	var got businesshours.BusinessHoursList
	if err := json.Decode(ctx.ResponseBody(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 7 {
		t.Fatalf("expected 7 decoded rows, got %d", len(got))
	}
	if !got[1].IsOpen || got[1].OpenTime != "08:00" {
		t.Errorf("Monday decoded incorrectly: %+v", got[1])
	}
}

func TestMountOperations_GetBusinessHours_Empty(t *testing.T) {
	m, _, _ := setup(t) // sin seedWeek — la tabla queda vacía

	reg := &mock.Router{}
	reg.Configure(mock.Config{
		Authorize: func(userID string, r model.Resource, a model.Action) bool { return true },
	})
	m.MountOperations(reg)

	ctx := &mock.Context{}
	ctx.SetUserID("u1")
	reg.Invoke("OP", "/"+businesshours.OpGetBusinessHours, ctx)

	if ctx.Status != 404 {
		t.Fatalf("expected 404 for an empty schedule, got %d", ctx.Status)
	}
}
