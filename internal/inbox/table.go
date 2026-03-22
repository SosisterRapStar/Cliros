package inbox

import "github.com/SosisterRapStar/cliros/database"

func qualifiedInboxTable(d database.SQLDialect) string {
	if d == database.SQLDialectPostgres {
		return "public.inbox"
	}
	return "inbox"
}
