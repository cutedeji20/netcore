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
