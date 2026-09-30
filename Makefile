DB_URL  ?= postgres://postgres:password@localhost:5432/worm_demo?sslmode=disable
COMPOSE ?= docker compose

up:
	$(COMPOSE) up -d postgres
	@until $(COMPOSE) exec -T postgres pg_isready -U postgres -d worm_demo >/dev/null 2>&1; do sleep 1; done

down:
	$(COMPOSE) down

# wipes and rebuilds the schema, then seeds it - see server/cmd/seed.
# needs DDL rights, so runs as the postgres superuser (DB_URL), never as
# demo_writer.
seed:
	cd server && SEED_CONN_STR="$(DB_URL)" go run ./cmd/seed

# creates the restricted demo_writer role + grants - run once per fresh
# postgres instance, after seed. edit server/sql/restricted_role.sql to
# set a real password first.
role:
	psql "$(DB_URL)" -f ./server/sql/restricted_role.sql

.PHONY: up down seed role
