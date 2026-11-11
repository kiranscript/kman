# Store fixtures

One SQLite database per release tag, `<tag>.db`, written by
`go run ./internal/store/fixturegen --version <tag>` and committed before the
tag is pushed (release checklist). A released fixture never changes.

`TestEveryFixtureMigrates` (`internal/store/fixtures_test.go`) upgrades a copy
of each one to the current schema and checks its data survived.
