# A makefile is essentially a text file which contains one or more rules 
# that the make utility can run. Each rule has a target and contains a sequence of 
# sequential commands which are executed when the rule is run.

# Generally speaking, makefile rules have the following structure:

# comment (optional) 
# target: prerequisite-target-1 prerequisite-target-2 ...
#    command 
#    command
#   ...

# When you specify a prerequisite target for a rule, the corresponding commands 
# for the prerequisite targets will be run before executing the actual target commands

# Important: 
# Please note that each command in a makefile rule must start with a tab character, not spaces. 

# To run a makefile rule, you can use the following command:
# make <rule-name>

# ${VAR_NAME} is used to reference a variable in a makefile.

# When we execute a make rule, every environment variable that is available to make 
# when it starts is transformed into a make variable with the same name and value.
# We can then access these variables using the syntax ${VARIABLE_NAME} in our makefile.


# Passing arguments
# The make utility also allows you to pass named arguments when executing a particular rule.
# To pass arguments to a make rule, you can use the following syntax:
# make <rule-name> arg_name1=value1 arg_name2=value2 ...

# The syntax to access the value of named arguments is exactly the same as for accessing 
# environment variables. So, in the example above, we could access the arg_name1
# via ${arg_name1} in our makefile.


# Namespacing targets
# As your makefile continues to grow, you might want to start namespacing your 
# target names to provide some differentiation between rules and help organize the file. 
# For example, in a large makefile rather than having the target name up it would be 
# clearer to give it the name db/migrations/up instead.
# make run/api

# MAKEFILE_LIST is a special variable which contains the name of the makefile being parsed by make.


# If you run make without specifying a target then it will default to 
# executing the first rule in the file.


# Phony targets
# We've been using make to execute actions, but another (and arguably, the primary) 
# purpose of make is to help create files on disk where the name of a target is the 
# name of a file being created by the rule.
# If you're using make primarily to execute actions, like we are, then this can 
# cause a problem if there is a file in your project directory with the same path as 
# a target name.

# To work around this, we can declare our makefile targets to be phony targets:
# A phony target is one that is not really the name of a file; rather it is just a 
# name for a rule to be executed.

# To declare a target as phony, you can make it prerequisite of the special .PHONY target. 
# The syntax looks like this:

# .PHONY: target
# target: prerequisite-target-1 prerequisite-target-2 ...
# 	command 
#	command 
#   ...


# @ character in your makefile to suppress that command from being echoed.

# To load and include env variables
include .envrc

# ==================================================================================== #
# HELPERS
# ==================================================================================== #

## help: print this help message
.PHONY: help
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed -e 's/^//'

.PHONY: confirm
confirm:
	@echo 'Are you sure? [y/N]' && read ans && [ $${ans:-N} = y ]

# ==================================================================================== #
# DEVELOPMENT
# ==================================================================================== #

## run/api: run the cmd/api application
.PHONY: run/api
run/api:
	@go run ./cmd/api -db-dsn=${GREENLIGHT_DB_DSN}

## db/psql: connect to the database using psql
.PHONY: db/sql
db/psql:
	psql ${GREENLIGHT_DB_DSN}

## db/migrations/new name=$1: create a new database migration
.PHONY: db/migrations/new
db/migrations/new:
	@echo 'Creating migration files for ${name}'
	migrate create -seq -ext=.sql -dir=./migrations ${name}

## db/migrations/up: apply all up database migrations
.PHONY: db/migrations/up
db/migrations/up: confirm
	@echo 'Running up migrations...'
	migrate -path="./migrations" -database "postgres://greenlight:${DB_PW}@localhost/greenlight?sslmode=disable" up

# ==================================================================================== #
# QUALITY CONTROL
# ==================================================================================== #

## audit: tidy dependencies and format, vet, and test all code
.PHONY: audit
audit: vendor
	@echo 'Formatting code...'
	go fmt ./...
	@echo 'Vetting code...'
	go vet ./...
	staticcheck ./...
	@echo 'Running tests...'
	go test -race -vet=off ./...

## vendor: tidy and vendor dependencies
.PHONY: vendor
vendor:
	@echo 'Tidying and verifying module dependencies'
	go mod tidy
	go mod verify
	@echo 'Vendoring dependencies'
	go mod vendor

# ==================================================================================== #
# BUILD
# ==================================================================================== #

## build/api: build the cmd/api application
.PHONY: build/api
build/api:
	@echo 'Building cmd/api...'
	go build -ldflags '-s -w' -o ./bin/api ./cmd/api
	GOOS=linux GOARCH=amd64 go build -ldflags='-s -w' -o=./bin/linux_amd64/api ./cmd/api

# ==================================================================================== #
# PRODUCTION
# ==================================================================================== #

production_host_ip = '144.126.210.226'

## production/connect: connect to the production server
.PHONY: production/connect
production/connect:
	ssh greenlight@${production_host_ip}

## production/deploy/api: deploy the api to production
.PHONY: production/deploy/api
production/deploy/api:
	rsync -P ./bin/linux_amd64/api greenlight@${production_host_ip}:~
	rsync -rP --delete ./migrations greenlight@${production_host_ip}:~
	rsync -P ./remote/production/api.service greenlight@${production_host_ip}:~
	rsync -P ./remote/production/Caddyfile greenlight@${production_host_ip}:~
	ssh -t greenlight@${production_host_ip} '\
		migrate -path ~/migrations -database $$GREENLIGHT_DB_DSN up \
        && sudo mv ~/api.service /etc/systemd/system/ \
        && sudo systemctl enable api \
        && sudo systemctl restart api \
        && sudo mv ~/Caddyfile /etc/caddy/ \
        && sudo systemctl reload caddy \
      '