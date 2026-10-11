package discord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

// Discord's JSON error codes for the two ways a read can come back empty.
const (
	errUnknownApplicationCommand            = 10063
	errUnknownApplicationCommandPermissions = 10066
)

var commandPermissionTypes = map[string]discordgo.ApplicationCommandPermissionType{
	"role":    discordgo.ApplicationCommandPermissionTypeRole,
	"user":    discordgo.ApplicationCommandPermissionTypeUser,
	"channel": discordgo.ApplicationCommandPermissionTypeChannel,
}

func resourceDiscordApplicationCommandPermissions() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceApplicationCommandPermissionsWrite,
		ReadContext:   resourceApplicationCommandPermissionsRead,
		UpdateContext: resourceApplicationCommandPermissionsWrite,
		DeleteContext: resourceApplicationCommandPermissionsDelete,
		Importer: &schema.ResourceImporter{
			StateContext: resourceApplicationCommandPermissionsImport,
		},

		Description: "Who may use an application's slash commands in a server — what Server Settings → Integrations edits. " +
			"The resource owns the whole list for its command: anything set by hand that is not in the configuration is removed on the next apply.\n\n" +
			"Reading works with the provider's bot token, for any application's commands. " +
			"**Writing does not**: Discord accepts only an OAuth2 Bearer token for it, from a user who may manage the server and its roles. " +
			"Set `client_id` and `secret` on the provider to an application owned by such a user; the provider then uses the client-credentials grant, which yields a token for the owner. " +
			"A team-owned application does not work, since the team is not a member of the server.",

		Schema: map[string]*schema.Schema{
			"server_id": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "ID of the server.",
			},
			"application_id": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "ID of the application that owns the command. It need not be the provider's own.",
			},
			"command_id": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "ID of the command. Use the `application_id` to set the defaults for all of the application's commands. Re-registering a command under the same name keeps its ID.",
			},
			"permission": {
				Type:        schema.TypeSet,
				Optional:    true,
				MaxItems:    100,
				Description: "One override. Without any, the command falls back to the application's defaults and the command's `default_member_permissions`.",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"type": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validation.StringInSlice([]string{"role", "user", "channel"}, false),
							Description:  "`role`, `user` or `channel`.",
						},
						"id": {
							Type:        schema.TypeString,
							Required:    true,
							Description: "ID of the role, user or channel. The server ID is the `@everyone` role; the server ID minus one is every channel.",
						},
						"allow": {
							Type:        schema.TypeBool,
							Required:    true,
							Description: "`true` to allow, `false` to deny.",
						},
					},
				},
			},
		},
	}
}

func commandPermissionsID(serverID, appID, commandID string) string {
	return serverID + ":" + appID + ":" + commandID
}

func resourceApplicationCommandPermissionsImport(ctx context.Context, d *schema.ResourceData, m interface{}) ([]*schema.ResourceData, error) {
	parts := strings.Split(d.Id(), ":")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, fmt.Errorf("expected an ID of the form server_id:application_id:command_id, got %q", d.Id())
	}
	d.Set("server_id", parts[0])
	d.Set("application_id", parts[1])
	d.Set("command_id", parts[2])
	return []*schema.ResourceData{d}, nil
}

func buildCommandPermissions(d *schema.ResourceData) []*discordgo.ApplicationCommandPermissions {
	set := d.Get("permission").(*schema.Set).List()
	perms := make([]*discordgo.ApplicationCommandPermissions, 0, len(set))
	for _, v := range set {
		p := v.(map[string]interface{})
		perms = append(perms, &discordgo.ApplicationCommandPermissions{
			ID:         p["id"].(string),
			Type:       commandPermissionTypes[p["type"].(string)],
			Permission: p["allow"].(bool),
		})
	}
	return perms
}

func flattenCommandPermissions(perms []*discordgo.ApplicationCommandPermissions) []interface{} {
	out := make([]interface{}, 0, len(perms))
	for _, p := range perms {
		name := ""
		for k, t := range commandPermissionTypes {
			if t == p.Type {
				name = k
			}
		}
		out = append(out, map[string]interface{}{
			"type":  name,
			"id":    p.ID,
			"allow": p.Permission,
		})
	}
	return out
}

func writeCommandPermissions(ctx context.Context, m *Context, d *schema.ResourceData, perms []*discordgo.ApplicationCommandPermissions) error {
	session, err := m.BearerSession(ctx)
	if err != nil {
		return err
	}
	return session.ApplicationCommandPermissionsEdit(
		d.Get("application_id").(string),
		d.Get("server_id").(string),
		d.Get("command_id").(string),
		&discordgo.ApplicationCommandPermissionsList{Permissions: perms},
		discordgo.WithContext(ctx),
	)
}

func resourceApplicationCommandPermissionsWrite(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	if err := writeCommandPermissions(ctx, m.(*Context), d, buildCommandPermissions(d)); err != nil {
		return diag.Errorf("Failed to set application command permissions: %s", err.Error())
	}
	d.SetId(commandPermissionsID(d.Get("server_id").(string), d.Get("application_id").(string), d.Get("command_id").(string)))
	return resourceApplicationCommandPermissionsRead(ctx, d, m)
}

func resourceApplicationCommandPermissionsRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Context).Session

	got, err := client.ApplicationCommandPermissions(
		d.Get("application_id").(string),
		d.Get("server_id").(string),
		d.Get("command_id").(string),
		discordgo.WithContext(ctx),
	)
	switch code := restErrorCode(err); {
	case err == nil:
		d.Set("permission", flattenCommandPermissions(got.Permissions))
	case code == errUnknownApplicationCommandPermissions:
		// The command exists but nothing overrides it — someone cleared the
		// list by hand. Keep the resource so the plan puts it back.
		d.Set("permission", []interface{}{})
	case code == errUnknownApplicationCommand:
		// The command is gone, and the permissions with it.
		d.SetId("")
	default:
		return diag.Errorf("Failed to read application command permissions: %s", err.Error())
	}
	return nil
}

func resourceApplicationCommandPermissionsDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	err := writeCommandPermissions(ctx, m.(*Context), d, []*discordgo.ApplicationCommandPermissions{})
	if code := restErrorCode(err); err != nil && code != errUnknownApplicationCommand {
		return diag.Errorf("Failed to clear application command permissions: %s", err.Error())
	}
	return nil
}

// restErrorCode is Discord's JSON error code, or 0 for anything else. A 404
// without a body counts as the command being unknown.
func restErrorCode(err error) int {
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) {
		return 0
	}
	if restErr.Message != nil && restErr.Message.Code != 0 {
		return restErr.Message.Code
	}
	if restErr.Response != nil && restErr.Response.StatusCode == http.StatusNotFound {
		return errUnknownApplicationCommand
	}
	return 0
}
