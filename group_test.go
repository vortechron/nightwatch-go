package nightwatch

import "testing"

func TestRequestGroup_MatchesPHPVectors(t *testing.T) {
	tests := []struct {
		name    string
		methods []string
		domain  string
		path    string
		want    string
	}{
		{"go style", []string{"GET"}, "", "/api/tasks/:id", "1acd506d40cd9665839b9d1e8d7c1725"},
		{"laravel style sorted join", []string{"HEAD", "GET"}, "", "/users/{id}", "cb9d0e2ef27216c86b2826721e15e03f"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RequestGroup(tt.methods, tt.domain, tt.path); got != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}

func TestQueryGroup(t *testing.T) {
	tests := []struct {
		name, driver, conn, sql, want string
	}{
		{"plain", "pgsql", "pgsql", "select * from tasks where id = ?", "907a86d90e53b336557d0cb9d70c9958"},
		{"unknown driver keeps sql", "mongo", "m", "select 1", GroupHash("m,select 1")},
		{"in clause", "mysql", "c", "select * from t where id in (1, 2, 3)", GroupHash("c,select * from t where id in (...?)")},
		{"in clause placeholders", "sqlite", "c", "select * from t where id in (?, ?)", GroupHash("c,select * from t where id in (...?)")},
		{"insert values", "mysql", "c", "insert into t (a) values (?, ?), (?, ?)", GroupHash("c,insert into t (a) values ...")},
		{"values without insert untouched", "mysql", "c", "select values (1)", GroupHash("c,select values (1)")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := QueryGroup(tt.driver, tt.conn, tt.sql); got != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}

func TestExceptionGroup(t *testing.T) {
	got := ExceptionGroup("main.ErrNotFound", "", "/app/handlers/task.go", 42)
	if want := "9bb563b3a9618d10cbfb3b0fa929d4ce"; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestScheduledTaskGroup(t *testing.T) {
	if got, want := ScheduledTaskGroup("n", "* * * * *", "UTC", 0), GroupHash("n,* * * * *,UTC"); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if got, want := ScheduledTaskGroup("n", "* * * * *", "UTC", 5), GroupHash("n,* * * * *,UTC,5"); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestSimpleGroups(t *testing.T) {
	if CacheGroup("redis", "k") != GroupHash("redis,k") {
		t.Fatal("cache group")
	}
	if NameGroup("jobs.Send") != GroupHash("jobs.Send") {
		t.Fatal("name group")
	}
}
