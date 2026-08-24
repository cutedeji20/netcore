package production

import (
	"os"
	"strings"
	"testing"
)

func requireContains(t *testing.T, source, want string) {
	t.Helper()
	if !strings.Contains(source, want) {
		t.Fatalf("expected configuration to contain %q", want)
	}
}

func requireNotContains(t *testing.T, source, unwanted string) {
	t.Helper()
	if strings.Contains(source, unwanted) {
		t.Fatalf("configuration must not contain %q", unwanted)
	}
}

func TestProductionRedisHasDedicatedStartupSecret(t *testing.T) {
	compose, err := os.ReadFile("compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	redisDockerfile, err := os.ReadFile("Dockerfile.redis")
	if err != nil {
		t.Fatal(err)
	}

	composeText := string(compose)

	// Redis must not share the application runtime directory: its startup user
	// is intentionally separate from the API/worker user.
	requireContains(t, composeText, "/redis/redis_password:/run/netcore/runtime/redis_password:ro")
	requireNotContains(t, composeText, "/app/redis_password:/run/netcore/runtime/redis_password:ro")
	requireContains(t, composeText, "cap_add: [\"CHOWN\", \"SETGID\", \"SETUID\"]")
	requireContains(t, string(redisDockerfile), "USER root")
}

func TestRedisWritesItsConfigurationBeforeDroppingDirectoryAccess(t *testing.T) {
	entrypoint, err := os.ReadFile("redis-entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	entrypointText := string(entrypoint)

	writeConfig := strings.Index(entrypointText, "cat > /run/redis/netcore.conf")
	chownRuntime := strings.Index(entrypointText, "chown redis:redis")
	if writeConfig == -1 || chownRuntime == -1 {
		t.Fatal("redis entrypoint is missing its configuration write or ownership handoff")
	}
	if writeConfig > chownRuntime {
		t.Fatal("redis must write its configuration before handing the runtime directory to the redis account")
	}
}

func TestMigrationRunnerUsesPsqlInputForVersionVariables(t *testing.T) {
	migrationRunner, err := os.ReadFile("migrations/migrate.sh")
	if err != nil {
		t.Fatal(err)
	}
	migrationText := string(migrationRunner)

	requireContains(t, migrationText, "SELECT EXISTS (SELECT 1 FROM netcore_schema_migrations WHERE version = :'version');")
	requireContains(t, migrationText, "INSERT INTO netcore_schema_migrations (version) VALUES (:'version');")
	requireNotContains(t, migrationText, "--set=\"version=$version\" -c")
}

func TestMigrationRoleGrantsDoNotRequireSuperuser(t *testing.T) {
	roleGrants, err := os.ReadFile("migrations/bootstrap_roles.sql")
	if err != nil {
		t.Fatal(err)
	}
	roleGrantsText := string(roleGrants)

	requireContains(t, roleGrantsText, "GRANT netcore_app_rw TO netcore_api;")
	requireContains(t, roleGrantsText, "GRANT netcore_radius TO netcore_radius_login;")
	requireNotContains(t, roleGrantsText, "ALTER ROLE netcore_api NOSUPERUSER")
	requireNotContains(t, roleGrantsText, "ALTER ROLE netcore_radius_login NOSUPERUSER")
}

func TestCaddyTrustedProxyAddressIsOutsideTheDynamicPool(t *testing.T) {
	compose, err := os.ReadFile("compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	composeText := string(compose)

	requireContains(t, composeText, "NETCORE_TRUSTED_PROXIES: 172.30.0.2")
	requireContains(t, composeText, "ipv4_address: 172.30.0.2")
	requireContains(t, composeText, "ip_range: 172.30.0.128/25")
}

func TestCaddyRoutesPublicCustomerAccountEndpointsToAPI(t *testing.T) {
	caddy, err := os.ReadFile("Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	requireContains(t, string(caddy), "/portal/auth/*")
}

func TestProductionPostgresUsesSeparateBootstrapSuperuser(t *testing.T) {
	compose, err := os.ReadFile("compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	postgresInit, err := os.ReadFile("postgres-init/010-create-login-roles.sh")
	if err != nil {
		t.Fatal(err)
	}

	composeText := string(compose)
	initText := string(postgresInit)

	// The database owner is deliberately non-superuser. A separate bootstrap-only
	// PostgreSQL role retains superuser power, so a first deployment cannot attempt
	// to revoke the only superuser and leave a partially initialized cluster.
	requireContains(t, composeText, "POSTGRES_USER: netcore_bootstrap")
	requireContains(t, composeText, "postgres_bootstrap_password")
	requireContains(t, initText, "CREATE ROLE netcore_owner LOGIN NOSUPERUSER")
	requireContains(t, initText, "ALTER DATABASE netcore OWNER TO netcore_owner;")
	requireContains(t, initText, "ALTER SCHEMA public OWNER TO netcore_owner;")
	requireNotContains(t, initText, "ALTER ROLE netcore_owner NOSUPERUSER")
}

func TestWorkerReceivesOnlyLogicalResendConfiguration(t *testing.T) {
	compose, err := os.ReadFile("compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	composeText := string(compose)
	workerStart := strings.Index(composeText, "  worker:")
	radiusStart := strings.Index(composeText, "  radius-spool-init:")
	if workerStart == -1 || radiusStart == -1 || radiusStart <= workerStart {
		t.Fatal("worker section is not bounded by the radius service")
	}
	worker := composeText[workerStart:radiusStart]
	requireContains(t, worker, "NETCORE_EMAIL_PROVIDER: ${NETCORE_EMAIL_PROVIDER:-disabled}")
	requireContains(t, worker, "NETCORE_RESEND_API_KEY_REF: ${NETCORE_RESEND_API_KEY_REF:-}")
	requireContains(t, worker, "NETCORE_EMAIL_FROM: ${NETCORE_EMAIL_FROM:-}")
	requireContains(t, worker, "/app:/run/netcore/runtime:ro")
	requireNotContains(t, worker, "NETCORE_RESEND_API_KEY:")
}

func TestReceiptOutboxMigrationScopesGlobalWorkerOperations(t *testing.T) {
	migration, err := os.ReadFile("../../db/migrations/0033_receipt_outbox_delivery.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(migration)
	for _, want := range []string{
		"CREATE FUNCTION payment_receipt_claim",
		"SECURITY DEFINER",
		"REVOKE ALL ON FUNCTION payment_receipt_claim(integer) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION payment_receipt_claim(integer) TO netcore_app_rw",
	} {
		requireContains(t, text, want)
	}
}
