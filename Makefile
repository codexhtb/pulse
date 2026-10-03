SHELL := /bin/bash

.PHONY: install up down restart status logs doctor backup restore upgrade config test-installer-ownership test-doctor-ports

install:
	sudo ./install.sh

up:
	docker compose up -d

down:
	docker compose down

restart:
	docker compose restart

status:
	docker compose ps

logs:
	docker compose logs -f --tail=200

doctor:
	./scripts/doctor.sh

backup:
	./scripts/backup.sh

restore:
	@test -n "$(BACKUP)" || { echo "Usage: make restore BACKUP=/path/to/backup.tar.gz" >&2; exit 2; }
	./scripts/restore.sh --backup "$(BACKUP)"

upgrade:
	./scripts/upgrade.sh

config:
	docker compose config

test-installer-ownership:
	sudo ./scripts/test-installer-ownership.sh

test-doctor-ports:
	./scripts/test-doctor-ports.sh
