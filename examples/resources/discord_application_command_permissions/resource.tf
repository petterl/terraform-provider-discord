# Writing needs a Bearer token, so the provider gets an application's client
# credentials. The application must be owned by a user who may manage the
# server and its roles; reading works with the bot token alone.
provider "discord" {
  token     = var.discord_token
  client_id = var.discord_client_id
  secret    = var.discord_client_secret
}

# Hide /report from everyone but moderators and one channel.
resource "discord_application_command_permissions" "report" {
  server_id      = var.server_id
  application_id = var.bot_application_id
  command_id     = var.report_command_id

  permission {
    type  = "role"
    id    = var.server_id # @everyone
    allow = false
  }

  permission {
    type  = "role"
    id    = var.moderator_role_id
    allow = true
  }

  permission {
    type  = "channel"
    id    = var.mod_channel_id
    allow = true
  }
}
