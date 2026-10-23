provider "roblox" {
  api_key = env("ROBLOX_API_KEY")
}

resource "roblox_experience" "main" {
  universe_id = "10438196190"
}

# Experience Config (BETA): key/value settings your game reads at runtime with
# ConfigService:GetConfigAsync() — feature flags and tuning values you can change
# without republishing the place. Needs universe:read + universe:write.
#
# The resource owns the repository's keys: a key that isn't listed here is
# removed on apply. Conditional (targeted) values set up in Creator Hub are kept.
# To adopt a game that already has configs, import it first so the plan shows
# exactly what would change:
#
#   import {
#     to = roblox_experience_config.flags
#     id = "10438196190"
#   }
#
# `destroy` removes every plain key and publishes (conditional rules are kept).
# Protect production config with `lifecycle { prevent_destroy = true }`.
resource "roblox_experience_config" "flags" {
  universe_id = roblox_experience.main.id # resolved at apply time

  # Values are native HCL: bool, number, string, or an object/list (a JSON config).
  entries = {
    enable_new_tutorial = true
    boss_health         = 100
    drop_rate           = 0.15
    motd                = "Double XP this weekend!"
    shop = {
      discount = 0.2
      featured = ["sword", "shield"]
    }
  }

  # "Immediate" (default) or "GradualRollout" (~15 minutes).
  deployment_strategy = "Immediate"
}
