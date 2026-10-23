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
}

# A standalone image asset, owned by a user or group.
resource "roblox_asset" "gem_icon" {
  file            = "examples/gem.png"
  display_name    = "Gem Icon"
  description     = "Currency icon uploaded by Baseplate"
  creator_user_id = 0 # your Roblox user id (or use creator_group_id)
}

# A developer product whose universe_id is a REFERENCE to the experience above —
# resolved at apply time, after the experience is reconciled.
resource "roblox_developer_product" "gems_100" {
  universe_id = roblox_experience.main.id
  name        = "100 Gems"
  price       = 25
  description = "Buy 100 gems"
}
