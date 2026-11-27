package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"

	"root-backend-service/internal/adapters/repository/postgres"
)

// baselineVersion es la primera migración: el esquema que, hasta que existió este paquete,
// cada repositorio creaba o alteraba en cada arranque con su InitSchema.
const baselineVersion int64 = 1

type schemaStep struct {
	name string
	run  func(context.Context) error
}

// baselineSteps devuelve los InitSchema en el mismo orden en que los corría cmd/api/main.go.
// Todas sus sentencias son idempotentes (IF NOT EXISTS, DROP/ADD de constraints, DO $$ ... $$),
// así que en una base que ya las corrió no cambian el esquema: solo queda registrada la versión 1.
//
// Estos InitSchema quedan CONGELADOS: no se editan nunca más. Un cambio de esquema nuevo va en
// un archivo sql/NNNNN_descripcion.sql (ver README.md); si no, las bases existentes no lo verían.
func baselineSteps(db *sql.DB) []schemaStep {
	return []schemaStep{
		{"messages", postgres.NewMessageRepository(db).InitSchema},
		{"push", postgres.NewPushRepository(db).InitSchema},
		{"notifications", postgres.NewNotificationRepository(db).InitSchema},
		{"password recovery", postgres.NewPasswordRecoveryRepository(db).InitSchema},
		{"matcher chat indexes", postgres.NewMatchRepository(db).InitSchema},
		{"events", postgres.NewEventRepository(db).InitSchema},
		{"artists", postgres.NewArtistRepository(db).InitSchema},
		{"attendance", postgres.NewBadgeRepository(db).InitSchema},
		{"surveys", postgres.NewEventSurveyRepository(db).InitSchema},
		{"communities", postgres.NewCommunityRepository(db).InitSchema},
		{"dance/crews", postgres.NewDanceRepository(db).InitSchema},
		{"temporary crews", postgres.NewTempCrewRepository(db).InitSchema},
	}
}

// baselineMigration corre sin una transacción común (RunDB): cada InitSchema abre la suya,
// igual que al arrancar antes. Si uno falla, la versión 1 no se registra y el próximo arranque
// lo reintenta entero, lo cual es seguro porque todo es idempotente.
func baselineMigration() *goose.Migration {
	return goose.NewGoMigration(baselineVersion, &goose.GoFunc{
		RunDB: func(ctx context.Context, db *sql.DB) error {
			for _, step := range baselineSteps(db) {
				if err := step.run(ctx); err != nil {
					return fmt.Errorf("baseline %s schema: %w", step.name, err)
				}
			}
			return nil
		},
	}, nil)
}
