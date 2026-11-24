# Example Baseplate config for a Roblox experience.
#
# Set ROBLOX_API_KEY in your environment (e.g. via a gitignored .env) and
# run `baseplate plan` / `baseplate apply`. Replace the placeholder IDs below.

provider "roblox" {
  api_key = env("ROBLOX_API_KEY")
}

# Adopts an existing universe (created in Creator Hub) and manages its writable
# config. The resource id is the universe id.
resource "roblox_experience" "main" {
  universe_id        = "YOUR_UNIVERSE_ID"
  voice_chat_enabled = true
  desktop_enabled    = true
  console_enabled    = false
  vr_enabled         = false

  # Private servers: a price enables them (0 = free, else 10-500 Robux);
  # private_servers_enabled = false disables them.
  private_server_price = 50

  # Social links: set both uri and title to create one; uri = "" removes it.
  discord_link_uri   = "https://discord.gg/your-invite"
  discord_link_title = "Join our Discord"
}

# Configures an existing place. On the root place, display_name/description are
# also the experience's name and description (read-only on the universe API).
resource "roblox_place_config" "root" {
  universe_id  = roblox_experience.main.id
  place_id     = "YOUR_ROOT_PLACE_ID"
  display_name = "My Experience"
  description  = "Managed by Baseplate"
  server_size  = 20
}

# A standalone image asset, owned by a user or group.
resource "roblox_asset" "gem_icon" {
  file            = "examples/gem.png"
  display_name    = "Gem Icon"
  description     = "Currency icon uploaded by Baseplate"
  creator_user_id = 0 # your Roblox user id (or use creator_group_id)
}

# A developer product whose universe_id references the experience above. The
# reference is resolved at apply time, after the experience is reconciled.
resource "roblox_developer_product" "gems_100" {
  universe_id = roblox_experience.main.id
  name        = "100 Gems"
  price       = 25
  description = "Buy 100 gems"

  is_regional_pricing_enabled = true
}
