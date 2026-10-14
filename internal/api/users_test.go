package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"doriyamedia/internal/config"
	"doriyamedia/internal/db"
)

// These tests drive the real router over HTTP with a cookie jar, so routing,
// the auth middleware, the session cookie and the account-state checks are all
// exercised the way a browser would hit them.

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	store, err := db.Open(filepath.Join(cfg.DataDir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.DB.Close() })

	srv := New(store, cfg, nil, nil, nil, nil, "test")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

// browser is one caller with its own cookie jar, i.e. one device.
func browser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &http.Client{Jar: jar}
}

func call(t *testing.T, c *http.Client, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

// setupAdmin runs first-run setup and returns the admin's browser.
func setupAdmin(t *testing.T, base string) *http.Client {
	t.Helper()
	c := browser(t)
	if code, body := call(t, c, "POST", base+"/api/setup", map[string]string{
		"name": "admin", "password": "admin-pass",
	}); code != 200 {
		t.Fatalf("setup: code %d body %v", code, body)
	}
	return c
}

func userField(t *testing.T, body map[string]any, field string) any {
	t.Helper()
	u, ok := body["user"].(map[string]any)
	if !ok {
		t.Fatalf("response has no user object: %v", body)
	}
	return u[field]
}

// A visitor registers, gets in immediately (the default "open" policy), reaches
// the media but not the backend, and an admin can then disable, re-enable and
// finally delete the account.
func TestRegistrationAndTheAdminBoundary(t *testing.T) {
	_, ts := newTestServer(t)
	base := ts.URL

	// Before setup the server must not let a stranger claim it.
	if code, body := call(t, browser(t), "GET", base+"/api/bootstrap", nil); code != 200 || body["setup_needed"] != true {
		t.Fatalf("bootstrap before setup: code %d body %v", code, body)
	}
	if code, _ := call(t, browser(t), "POST", base+"/api/auth/register", map[string]string{
		"name": "squatter", "password": "hunter2",
	}); code != 400 {
		t.Fatalf("register before setup should be refused, got %d", code)
	}

	admin := setupAdmin(t, base)

	if code, body := call(t, browser(t), "GET", base+"/api/bootstrap", nil); code != 200 {
		t.Fatalf("bootstrap: code %d body %v", code, body)
	} else if body["setup_needed"] != false || body["can_register"] != true {
		t.Fatalf("bootstrap after setup: %v", body)
	}

	// A second person signs up and is signed in by the same request.
	alice := browser(t)
	code, body := call(t, alice, "POST", base+"/api/auth/register", map[string]string{
		"name": "alice", "password": "alice-pass",
	})
	if code != 200 {
		t.Fatalf("register: code %d body %v", code, body)
	}
	if got := userField(t, body, "role"); got != "user" {
		t.Errorf("self-registered role = %v, want user", got)
	}
	if got := userField(t, body, "status"); got != "active" {
		t.Errorf("self-registered status = %v, want active", got)
	}
	// Registering signs you in, so the account must not look untouched.
	if got := userField(t, body, "last_login_at"); got == nil {
		t.Error("last_login_at is empty right after registering and being signed in")
	}

	// The admin's list carries the same information.
	code, body = call(t, admin, "GET", base+"/api/admin/users", nil)
	if code != 200 {
		t.Fatalf("admin list users: code %d body %v", code, body)
	}
	for _, raw := range body["users"].([]any) {
		u := raw.(map[string]any)
		if u["name"] == "alice" && u["last_login_at"] == nil {
			t.Error("the admin's user list shows alice as never logged in")
		}
		if _, leaked := u["pass_hash"]; leaked {
			t.Error("the user list leaked a password hash")
		}
	}

	// A viewer may watch, but the backend belongs to the admin alone.
	if code, body := call(t, alice, "GET", base+"/api/libraries", nil); code != 200 {
		t.Errorf("viewer GET /api/libraries: code %d body %v", code, body)
	}
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/config"},
		{"GET", "/api/admin/users"},
		{"POST", "/api/admin/users"},
		{"PUT", "/api/admin/users/1"},
		{"DELETE", "/api/admin/users/1"},
	} {
		if code, _ := call(t, alice, tc.method, base+tc.path, map[string]string{"name": "x", "password": "yyyy"}); code != 403 {
			t.Errorf("viewer %s %s = %d, want 403", tc.method, tc.path, code)
		}
	}
	if code, body := call(t, alice, "GET", base+"/api/auth/me", nil); code != 200 || body["is_admin"] != false {
		t.Errorf("viewer me: code %d body %v", code, body)
	}

	// The admin sees both accounts and alice's id.
	code, body = call(t, admin, "GET", base+"/api/admin/users", nil)
	if code != 200 {
		t.Fatalf("admin list users: code %d body %v", code, body)
	}
	aliceID := idOf(t, body, "alice")
	if n := len(body["users"].([]any)); n != 2 {
		t.Fatalf("user list has %d entries, want 2: %v", n, body)
	}

	// Disabling takes effect immediately: existing sessions stop working and a
	// fresh sign-in is refused.
	if code, body := call(t, admin, "PUT", base+"/api/admin/users/"+aliceID, map[string]string{"status": "disabled"}); code != 200 {
		t.Fatalf("disable: code %d body %v", code, body)
	}
	if code, _ := call(t, alice, "GET", base+"/api/auth/me", nil); code != 401 {
		t.Errorf("disabled account's live session = %d, want 401", code)
	}
	if code, _ := call(t, browser(t), "POST", base+"/api/auth/login", map[string]string{
		"name": "alice", "password": "alice-pass",
	}); code != 403 {
		t.Errorf("disabled account login = %d, want 403", code)
	}

	// Re-enabling restores access.
	if code, _ := call(t, admin, "PUT", base+"/api/admin/users/"+aliceID, map[string]string{"status": "active"}); code != 200 {
		t.Fatalf("re-enable failed")
	}
	alice = browser(t)
	if code, body := call(t, alice, "POST", base+"/api/auth/login", map[string]string{
		"name": "alice", "password": "alice-pass",
	}); code != 200 {
		t.Fatalf("login after re-enable: code %d body %v", code, body)
	}

	// A password change needs the current password and signs the other device out.
	other := browser(t)
	if code, _ := call(t, other, "POST", base+"/api/auth/login", map[string]string{
		"name": "alice", "password": "alice-pass",
	}); code != 200 {
		t.Fatalf("second device login failed")
	}
	if code, _ := call(t, alice, "POST", base+"/api/auth/password", map[string]string{
		"current_password": "wrong", "new_password": "alice-pass2",
	}); code != 403 {
		t.Errorf("password change with wrong current password = %d, want 403", code)
	}
	if code, body := call(t, alice, "POST", base+"/api/auth/password", map[string]string{
		"current_password": "alice-pass", "new_password": "alice-pass2",
	}); code != 200 {
		t.Fatalf("password change: code %d body %v", code, body)
	}
	if code, _ := call(t, alice, "GET", base+"/api/auth/me", nil); code != 200 {
		t.Errorf("the device that changed the password was signed out (%d)", code)
	}
	if code, _ := call(t, other, "GET", base+"/api/auth/me", nil); code != 401 {
		t.Errorf("the other device survived a password change (%d), want 401", code)
	}
	if code, _ := call(t, browser(t), "POST", base+"/api/auth/login", map[string]string{
		"name": "alice", "password": "alice-pass",
	}); code != 401 {
		t.Errorf("the old password still works (%d), want 401", code)
	}
	if code, _ := call(t, browser(t), "POST", base+"/api/auth/login", map[string]string{
		"name": "alice", "password": "alice-pass2",
	}); code != 200 {
		t.Errorf("the new password does not work (%d)", code)
	}

	// Deleting the account closes the door for good.
	if code, body := call(t, admin, "DELETE", base+"/api/admin/users/"+aliceID, nil); code != 200 {
		t.Fatalf("delete: code %d body %v", code, body)
	}
	if code, _ := call(t, browser(t), "POST", base+"/api/auth/login", map[string]string{
		"name": "alice", "password": "alice-pass2",
	}); code != 401 {
		t.Errorf("deleted account can still sign in (%d)", code)
	}
	if code, _ := call(t, admin, "DELETE", base+"/api/admin/users/1", nil); code != 400 {
		t.Errorf("admin deleted their own account (%d), want 400", code)
	}
}

// idOf finds a user's id in a list response by name.
func idOf(t *testing.T, body map[string]any, name string) string {
	t.Helper()
	list, _ := body["users"].([]any)
	for _, raw := range list {
		u, _ := raw.(map[string]any)
		if u["name"] == name {
			return jsonNumber(u["id"])
		}
	}
	t.Fatalf("user %q not found in %v", name, body)
	return ""
}

func jsonNumber(v any) string {
	f, _ := v.(float64)
	return strconvItoa(int64(f))
}

func strconvItoa(n int64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

// The registration policy is the admin's switch: approval queues a signup until
// someone lets it in, and closed refuses signup while still allowing accounts
// the admin creates.
func TestRegistrationPolicyModes(t *testing.T) {
	srv, ts := newTestServer(t)
	base := ts.URL
	admin := setupAdmin(t, base)

	// --- approval ---
	srv.Cfg.Users.Registration = config.RegistrationApproval
	bob := browser(t)
	code, body := call(t, bob, "POST", base+"/api/auth/register", map[string]string{
		"name": "bob", "password": "bob-pass",
	})
	if code != 202 || body["pending"] != true {
		t.Fatalf("register under approval: code %d body %v", code, body)
	}
	if code, _ := call(t, bob, "GET", base+"/api/auth/me", nil); code != 401 {
		t.Errorf("a pending signup was signed in (%d)", code)
	}
	if code, _ := call(t, browser(t), "POST", base+"/api/auth/login", map[string]string{
		"name": "bob", "password": "bob-pass",
	}); code != 403 {
		t.Errorf("pending account login = %d, want 403", code)
	}

	code, body = call(t, admin, "GET", base+"/api/admin/users", nil)
	if code != 200 {
		t.Fatalf("list users: %d", code)
	}
	bobID := idOf(t, body, "bob")
	// Approving is just a status change.
	if code, body := call(t, admin, "PUT", base+"/api/admin/users/"+bobID, map[string]string{"status": "active"}); code != 200 {
		t.Fatalf("approve: code %d body %v", code, body)
	}
	if code, _ := call(t, browser(t), "POST", base+"/api/auth/login", map[string]string{
		"name": "bob", "password": "bob-pass",
	}); code != 200 {
		t.Errorf("approved account cannot sign in (%d)", code)
	}

	// --- closed ---
	srv.Cfg.Users.Registration = config.RegistrationClosed
	if code, body := call(t, browser(t), "GET", base+"/api/bootstrap", nil); code != 200 || body["can_register"] != false {
		t.Errorf("bootstrap while closed: %v", body)
	}
	if code, _ := call(t, browser(t), "POST", base+"/api/auth/register", map[string]string{
		"name": "carol", "password": "carol-pass",
	}); code != 403 {
		t.Errorf("register while closed = %d, want 403", code)
	}
	// The admin can still add someone by hand.
	if code, body := call(t, admin, "POST", base+"/api/admin/users", map[string]string{
		"name": "carol", "password": "carol-pass", "role": "user",
	}); code != 200 {
		t.Fatalf("admin create user: code %d body %v", code, body)
	}
	if code, _ := call(t, browser(t), "POST", base+"/api/auth/login", map[string]string{
		"name": "carol", "password": "carol-pass",
	}); code != 200 {
		t.Errorf("admin-created account cannot sign in (%d)", code)
	}
}

// The server must always keep one usable admin: an admin can step down only
// once somebody else can take over.
func TestTheLastAdminCannotBeLockedOut(t *testing.T) {
	_, ts := newTestServer(t)
	base := ts.URL
	admin := setupAdmin(t, base)

	// Alone, the admin cannot demote themselves out of the panel.
	if code, body := call(t, admin, "PUT", base+"/api/admin/users/1", map[string]string{"role": "user"}); code != 400 {
		t.Fatalf("sole admin demoted itself: code %d body %v", code, body)
	}
	// Nor disable themselves.
	if code, _ := call(t, admin, "PUT", base+"/api/admin/users/1", map[string]string{"status": "disabled"}); code != 400 {
		t.Errorf("admin disabled itself (%d), want 400", code)
	}

	// With a second admin in place, stepping down is allowed.
	code, body := call(t, admin, "POST", base+"/api/admin/users", map[string]string{
		"name": "deputy", "password": "deputy-pass", "role": "admin",
	})
	if code != 200 {
		t.Fatalf("create deputy: code %d body %v", code, body)
	}
	deputyID := strconvItoa(int64(userField(t, body, "id").(float64)))

	code, body = call(t, admin, "PUT", base+"/api/admin/users/1", map[string]string{"role": "user"})
	if code != 200 {
		t.Fatalf("demote self with a deputy present: code %d body %v", code, body)
	}
	if code, _ := call(t, admin, "GET", base+"/api/admin/users", nil); code != 403 {
		t.Errorf("demoted admin still reaches the user list (%d), want 403", code)
	}

	// The deputy is now the only admin and is protected in turn.
	deputy := browser(t)
	if code, _ := call(t, deputy, "POST", base+"/api/auth/login", map[string]string{
		"name": "deputy", "password": "deputy-pass",
	}); code != 200 {
		t.Fatalf("deputy login failed")
	}
	if code, body := call(t, deputy, "DELETE", base+"/api/admin/users/1", nil); code != 200 {
		t.Fatalf("delete the demoted account: code %d body %v", code, body)
	}
	if code, _ := call(t, deputy, "PUT", base+"/api/admin/users/"+deputyID, map[string]string{"role": "user"}); code != 400 {
		t.Errorf("last admin demoted itself (%d), want 400", code)
	}
}

// A wrong password must not be free to try forever once signup is open.
func TestLoginIsThrottled(t *testing.T) {
	_, ts := newTestServer(t)
	base := ts.URL
	setupAdmin(t, base)

	c := browser(t)
	throttled := false
	for i := 0; i < 15; i++ {
		code, _ := call(t, c, "POST", base+"/api/auth/login", map[string]string{
			"name": "admin", "password": "wrong",
		})
		if code == 429 {
			throttled = true
			break
		}
		if code != 401 {
			t.Fatalf("attempt %d: code %d, want 401 or 429", i+1, code)
		}
	}
	if !throttled {
		t.Error("fifteen wrong passwords were never throttled")
	}
	// The correct password is refused too while the address is blocked, which is
	// the point of the throttle.
	if code, _ := call(t, c, "POST", base+"/api/auth/login", map[string]string{
		"name": "admin", "password": "admin-pass",
	}); code != 429 {
		t.Errorf("throttled address could sign in (%d)", code)
	}
}

// Account names are shown in the UI and written to logs, so the obvious junk is
// rejected before it reaches the database.
func TestRegistrationInputValidation(t *testing.T) {
	_, ts := newTestServer(t)
	base := ts.URL
	setupAdmin(t, base)

	cases := []struct {
		name, user, pass string
	}{
		{"too short", "a", "good-password"},
		{"whitespace inside", "bad name", "good-password"},
		{"punctuation", "bad/name", "good-password"},
		{"password too short", "shorty", "abc"},
		{"empty", "", "good-password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, body := call(t, browser(t), "POST", base+"/api/auth/register", map[string]string{
				"name": tc.user, "password": tc.pass,
			}); code != 400 {
				t.Errorf("code %d body %v, want 400", code, body)
			}
		})
	}

	// Taking a name that exists is refused, and only by exact spelling: a
	// look-alike "Admin" would be an impostor in the account list.
	if code, _ := call(t, browser(t), "POST", base+"/api/auth/register", map[string]string{
		"name": "admin", "password": "good-password",
	}); code != 409 {
		t.Errorf("duplicate name = %d, want 409", code)
	}
	if code, _ := call(t, browser(t), "POST", base+"/api/auth/register", map[string]string{
		"name": "Admin", "password": "good-password",
	}); code != 409 {
		t.Errorf("look-alike name = %d, want 409", code)
	}
	// Signing in does not care about the case either.
	if code, _ := call(t, browser(t), "POST", base+"/api/auth/login", map[string]string{
		"name": "ADMIN", "password": "admin-pass",
	}); code != 200 {
		t.Errorf("differently cased sign-in = %d, want 200", code)
	}
}
