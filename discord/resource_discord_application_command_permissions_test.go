package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// fakeDiscord answers the two endpoints the resource uses and records what it
// was sent. Every request to discord.com is rerouted to it by the client's
// transport, so the code under test builds its URLs exactly as in production.
type fakeDiscord struct {
	mu          sync.Mutex
	tokenCalls  int
	tokenForm   url.Values
	tokenAuth   [2]string
	puts        []string // Authorization header of each PUT
	putBody     []map[string]interface{}
	getStatus   int
	getBody     string
	permissions string // JSON stored by the last PUT, served by GET
}

func (f *fakeDiscord) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/oauth2/token"):
		f.tokenCalls++
		r.ParseForm()
		f.tokenForm = r.PostForm
		u, p, _ := r.BasicAuth()
		f.tokenAuth = [2]string{u, p}
		fmt.Fprint(w, `{"access_token":"owner-token","token_type":"Bearer","expires_in":604800,"scope":"applications.commands.permissions.update"}`)
	case strings.HasSuffix(r.URL.Path, "/permissions") && r.Method == http.MethodPut:
		f.puts = append(f.puts, r.Header.Get("Authorization"))
		raw, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		json.Unmarshal(raw, &body)
		f.putBody = append(f.putBody, body)
		perms, _ := json.Marshal(body["permissions"])
		f.permissions = string(perms)
		fmt.Fprintf(w, `{"id":"30","application_id":"20","guild_id":"10","permissions":%s}`, perms)
	case strings.HasSuffix(r.URL.Path, "/permissions") && r.Method == http.MethodGet:
		if f.getStatus != 0 {
			w.WriteHeader(f.getStatus)
			fmt.Fprint(w, f.getBody)
			return
		}
		perms := f.permissions
		if perms == "" {
			perms = "[]"
		}
		fmt.Fprintf(w, `{"id":"30","application_id":"20","guild_id":"10","permissions":%s}`, perms)
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusTeapot)
	}
}

type rerouteTransport struct{ target *url.URL }

func (t rerouteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme = t.target.Scheme
	r.URL.Host = t.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

func newFakeContext(t *testing.T, clientID, secret string) (*Context, *fakeDiscord) {
	t.Helper()
	fake := &fakeDiscord{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	cfg := &Config{
		Token:      "Bot bot-token",
		ClientID:   clientID,
		Secret:     secret,
		HTTPClient: &http.Client{Transport: rerouteTransport{target}},
	}
	ctx, err := cfg.Client("test")
	if err != nil {
		t.Fatal(err)
	}
	return ctx, fake
}

func commandPermissionsData(t *testing.T, perms []interface{}) *schema.ResourceData {
	t.Helper()
	return schema.TestResourceDataRaw(t, resourceDiscordApplicationCommandPermissions().Schema, map[string]interface{}{
		"server_id":      "10",
		"application_id": "20",
		"command_id":     "30",
		"permission":     perms,
	})
}

var exampleCommandPermissions = []interface{}{
	map[string]interface{}{"type": "role", "id": "10", "allow": false},
	map[string]interface{}{"type": "role", "id": "11", "allow": true},
	map[string]interface{}{"type": "channel", "id": "9", "allow": true},
}

func TestApplicationCommandPermissionsWriteUsesTheOwnersBearerToken(t *testing.T) {
	m, fake := newFakeContext(t, "app", "shh")
	d := commandPermissionsData(t, exampleCommandPermissions)

	if diags := resourceApplicationCommandPermissionsWrite(context.Background(), d, m); diags.HasError() {
		t.Fatalf("write: %v", diags)
	}

	if fake.tokenAuth != [2]string{"app", "shh"} {
		t.Errorf("token request authenticated as %v, want the provider's client_id and secret", fake.tokenAuth)
	}
	if got := fake.tokenForm.Get("grant_type"); got != "client_credentials" {
		t.Errorf("grant_type = %q", got)
	}
	if got := fake.tokenForm.Get("scope"); got != commandPermissionsScope {
		t.Errorf("scope = %q", got)
	}
	if len(fake.puts) != 1 || fake.puts[0] != "Bearer owner-token" {
		t.Fatalf("PUTs sent with %v, want one with the Bearer token — a bot token is refused", fake.puts)
	}

	var sent []string
	for _, p := range fake.putBody[0]["permissions"].([]interface{}) {
		p := p.(map[string]interface{})
		sent = append(sent, fmt.Sprintf("%v:%v:%v", p["type"], p["id"], p["permission"]))
	}
	sort.Strings(sent)
	want := []string{"1:10:false", "1:11:true", "3:9:true"}
	if fmt.Sprint(sent) != fmt.Sprint(want) {
		t.Errorf("sent %v, want %v", sent, want)
	}

	if d.Id() != "10:20:30" {
		t.Errorf("id = %q", d.Id())
	}
	if n := d.Get("permission").(*schema.Set).Len(); n != 3 {
		t.Errorf("read back %d permissions, want 3", n)
	}
}

func TestApplicationCommandPermissionsTokenIsFetchedOncePerRun(t *testing.T) {
	m, fake := newFakeContext(t, "app", "shh")
	for i := 0; i < 3; i++ {
		d := commandPermissionsData(t, exampleCommandPermissions)
		if diags := resourceApplicationCommandPermissionsWrite(context.Background(), d, m); diags.HasError() {
			t.Fatalf("write %d: %v", i, diags)
		}
	}
	if fake.tokenCalls != 1 {
		t.Errorf("token requested %d times, want 1", fake.tokenCalls)
	}
}

func TestApplicationCommandPermissionsWriteWithoutClientCredentialsSaysWhy(t *testing.T) {
	m, fake := newFakeContext(t, "", "")
	d := commandPermissionsData(t, exampleCommandPermissions)

	diags := resourceApplicationCommandPermissionsWrite(context.Background(), d, m)
	if !diags.HasError() || !strings.Contains(diags[0].Summary, "client_id") {
		t.Fatalf("want an error naming client_id, got %v", diags)
	}
	if fake.tokenCalls != 0 || len(fake.puts) != 0 {
		t.Errorf("nothing should reach Discord without credentials")
	}
}

func TestApplicationCommandPermissionsReadNeedsOnlyTheBotToken(t *testing.T) {
	// Plans and drift checks run without client credentials.
	m, fake := newFakeContext(t, "", "")
	fake.permissions = `[{"id":"10","type":1,"permission":false},{"id":"55","type":2,"permission":true}]`
	d := commandPermissionsData(t, nil)
	d.SetId("10:20:30")

	if diags := resourceApplicationCommandPermissionsRead(context.Background(), d, m); diags.HasError() {
		t.Fatalf("read: %v", diags)
	}
	got := map[string]bool{}
	for _, p := range d.Get("permission").(*schema.Set).List() {
		p := p.(map[string]interface{})
		got[p["type"].(string)+":"+p["id"].(string)] = p["allow"].(bool)
	}
	if fmt.Sprint(got) != fmt.Sprint(map[string]bool{"role:10": false, "user:55": true}) {
		t.Errorf("read %v", got)
	}
}

func TestApplicationCommandPermissionsClearedByHandIsKeptForThePlan(t *testing.T) {
	m, fake := newFakeContext(t, "", "")
	fake.getStatus = http.StatusNotFound
	fake.getBody = `{"message":"Unknown application command permissions","code":10066}`
	d := commandPermissionsData(t, exampleCommandPermissions)
	d.SetId("10:20:30")

	if diags := resourceApplicationCommandPermissionsRead(context.Background(), d, m); diags.HasError() {
		t.Fatalf("read: %v", diags)
	}
	if d.Id() == "" {
		t.Fatal("the resource was dropped; the plan would not put the permissions back")
	}
	if n := d.Get("permission").(*schema.Set).Len(); n != 0 {
		t.Errorf("read %d permissions, want 0", n)
	}
}

func TestApplicationCommandPermissionsVanishWithTheirCommand(t *testing.T) {
	m, fake := newFakeContext(t, "", "")
	fake.getStatus = http.StatusNotFound
	fake.getBody = `{"message":"Unknown application command","code":10063}`
	d := commandPermissionsData(t, exampleCommandPermissions)
	d.SetId("10:20:30")

	if diags := resourceApplicationCommandPermissionsRead(context.Background(), d, m); diags.HasError() {
		t.Fatalf("read: %v", diags)
	}
	if d.Id() != "" {
		t.Error("want the resource removed from state when its command is gone")
	}
}

func TestApplicationCommandPermissionsDeleteClearsTheList(t *testing.T) {
	m, fake := newFakeContext(t, "app", "shh")
	d := commandPermissionsData(t, exampleCommandPermissions)
	d.SetId("10:20:30")

	if diags := resourceApplicationCommandPermissionsDelete(context.Background(), d, m); diags.HasError() {
		t.Fatalf("delete: %v", diags)
	}
	if len(fake.putBody) != 1 || len(fake.putBody[0]["permissions"].([]interface{})) != 0 {
		t.Errorf("want one PUT of an empty list, got %v", fake.putBody)
	}
}

func TestApplicationCommandPermissionsImport(t *testing.T) {
	d := commandPermissionsData(t, nil)
	d.SetId("10:20:30")
	if _, err := resourceApplicationCommandPermissionsImport(context.Background(), d, nil); err != nil {
		t.Fatal(err)
	}
	if d.Get("server_id") != "10" || d.Get("application_id") != "20" || d.Get("command_id") != "30" {
		t.Errorf("import set %v %v %v", d.Get("server_id"), d.Get("application_id"), d.Get("command_id"))
	}
	for _, bad := range []string{"10:20", "10::30", "a:b:c:d"} {
		d.SetId(bad)
		if _, err := resourceApplicationCommandPermissionsImport(context.Background(), d, nil); err == nil {
			t.Errorf("import %q: want an error", bad)
		}
	}
}

func TestAccResourceDiscordApplicationCommandPermissions(t *testing.T) {
	serverID := os.Getenv("DISCORD_TEST_SERVER_ID")
	appID := os.Getenv("DISCORD_TEST_APPLICATION_ID")
	commandID := os.Getenv("DISCORD_TEST_COMMAND_ID")
	roleID := os.Getenv("DISCORD_TEST_ROLE_ID")
	if serverID == "" || appID == "" || commandID == "" || roleID == "" {
		t.Skip("DISCORD_TEST_SERVER_ID, DISCORD_TEST_APPLICATION_ID, DISCORD_TEST_COMMAND_ID and DISCORD_TEST_ROLE_ID must be set for acceptance tests")
	}
	name := "discord_application_command_permissions.example"
	resource.Test(t, resource.TestCase{
		PreCheck:          func() { testAccPreCheck(t) },
		ProviderFactories: providerFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource "discord_application_command_permissions" "example" {
				  server_id      = "%[1]s"
				  application_id = "%[2]s"
				  command_id     = "%[3]s"

				  permission {
				    type  = "role"
				    id    = "%[1]s"
				    allow = false
				  }
				  permission {
				    type  = "role"
				    id    = "%[4]s"
				    allow = true
				  }
				}`, serverID, appID, commandID, roleID),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "permission.#", "2"),
				),
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateId:     serverID + ":" + appID + ":" + commandID,
				ImportStateVerify: true,
			},
		},
	})
}
