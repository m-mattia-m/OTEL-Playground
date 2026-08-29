# OTEL-Playground

![coverage](https://raw.githubusercontent.com/m-mattia-m/OTEL-Playground/main/.badges/coverage.svg)

This is a basic setup of my OTEL playground. I want to explore and experiment with OpenTelemetry for future projects.
But it also includes some other services like an IdP.

## Services

> Start everything with `docker compose up --project-directory ./infrastructure -d`

- APP Postgres database -> `postgres://app:app@localhost:5432/app?sslmode=disable`
- Zitadel -> [http://localhost:8080](http://localhost:8080)
    - username: `admin@zitadel.localhost`
    - password: `Password1!`
    - Postgres database -> `postgres://postgres:postgres@localhost:5434/postgres?sslmode=disable`
- KeyCloak -> [http://localhost:8082](http://localhost:8082)
  - username: `admin`
  - password: `admin`
  - Postgres database -> `postgres://keycloak:keycloak@localhost:5433/keycloak?sslmode=disable`
- Grafana -> [http://localhost:3000](http://localhost:3000)
    - username: `admin`
    - password: `admin`
    - OTEL collector -> [http://localhost:4317](http://localhost:4317)
    - ClickHouse database -> [http://localhost:8123](http://localhost:8123)
- HyperDX -> [http://localhost:8081](http://localhost:8081)

## Grafana

> **Note for me**: The Grafana Dashboard does not look as good/clear as other platforms like Jaeger, HyperDX, SigNoz.

To install all the dashboards you want, you have to open Grafana and log in. Then you have to navigate to
`Connections` -> `Data sources` -> `ClickHouse` -> navigate to the tab `Dashboards` in the datasource itself -> click in
`import` for each dashboard you want to install. Then you can navigate to the `Dashboards` section (you might have to
refresh the site).

## Project structure

- api -> everything which is related with the API
  - controller -> the API / endpoint itself
  - response -> the response structure for the API endpoints
  - model -> the object definition for the API models
  - mapper -> the mapping logic between the API models and the underlying data structures
- config -> configuration model and logic to read and access the configuration
- internal -> everything in the core-application
  - domain -> the business-logic; should be split up by **use-case**
  - service -> the general abstracted logic; should be split up by **functionality**
  - repository -> the database connection including the whole data access logic
- infrastructure -> dev infrastructure for compose files and its configuration
- migrations -> database migrations
- utils -> utils and helper files which where useful in any layer