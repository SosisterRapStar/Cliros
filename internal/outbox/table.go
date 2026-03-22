package outbox

import "github.com/SosisterRapStar/cliros/database"

func qualifiedOutboxTable(d database.SQLDialect) string {
	if d == database.SQLDialectPostgres {
		return "public.outbox"
	}
	return "outbox"
}
