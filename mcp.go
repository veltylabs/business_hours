package businesshours

import (
	"webtyp.com/ddl"
	"webtyp.com/events"
	"webtyp.com/fmt"
	"webtyp.com/model"
	"webtyp.com/orm"
	"webtyp.com/router"
)

type Deps struct {
	IDs       model.IDGenerator // requerido — el módulo nunca lo construye por sí mismo
	Publisher events.Publisher  // opcional — nil deshabilita la publicación silenciosamente
}

type Module struct {
	db  *orm.DB
	ids model.IDGenerator
	pub events.Publisher
}

func New(db *orm.DB, deps Deps) (*Module, error) {
	if deps.IDs == nil {
		return nil, fmt.Err("business_hours: Deps.IDs es requerido")
	}
	// ddl.Compiler es una capacidad opcional — solo los backends de SQL (sqlt, postgres) la implementan.
	// storage/mem (las pruebas propias de este módulo, Etapa 6) crea tablas de forma perezosa (lazy) y no necesita
	// DDL en absoluto, por lo que una aserción de tipo, y no una llamada incondicional, es cómo el módulo se mantiene agnóstico aquí.
	if ddlCompiler, ok := db.RawConn().(ddl.Compiler); ok {
		if err := ddl.New(db.RawConn(), ddlCompiler).CreateTable(&BusinessHours{}); err != nil {
			return nil, err
		}
	}
	return &Module{db: db, ids: deps.IDs, pub: deps.Publisher}, nil
}

const OpGetBusinessHours = "get_business_hours"

func (m *Module) ModelName() string { return "business_hours" }

func (m *Module) MountOperations(reg router.OperationRegistry) {
	// Esta operación no acepta parámetros — Accepts(nil) es la declaración documentada para "sin argumentos"
	// (comentario de documentación de router.Route). Nunca invente una estructura de argumentos vacía para una operación sin argumentos.
	reg.Operation(OpGetBusinessHours, m.opGetBusinessHours).
		Requires("business_hours", model.Read).
		Accepts(nil)
}

var _ router.OperationModule = (*Module)(nil)

// GetSchedule devuelve las 7 filas ordenadas por day_of_week, o ErrNotFound si la tabla está vacía
// (sin sembrar — la aplicación de composición raíz es responsable de sembrar las 7 filas; ver AGENTS.md).
func (m *Module) GetSchedule() ([]BusinessHours, error) {
	rows, err := ReadAllBusinessHours(m.db.Query(&BusinessHours{}).OrderBy(BusinessHours_.DayOfWeek).Asc())
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	items := make([]BusinessHours, len(rows))
	for i, r := range rows {
		items[i] = *r
	}
	return items, nil
}

func (m *Module) opGetBusinessHours(ctx router.Context) {
	rows, err := m.GetSchedule()
	if err != nil {
		// Convención de estado (en todo el ecosistema): 404 para no encontrado, 500 solo para errores internos
		// genuinos — nunca colapse ambos en 500 (el "misterio de tiempo de ejecución" que el arnés prohíbe).
		if err == ErrNotFound {
			ctx.WriteStatus(404)
			return
		}
		ctx.WriteStatus(500)
		return
	}
	list := make(BusinessHoursList, len(rows))
	for i := range rows {
		list[i] = &rows[i]
	}
	if err := ctx.Encode(&list); err != nil {
		ctx.WriteStatus(500)
	}
}
