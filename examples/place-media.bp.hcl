provider "roblox" {
  api_key = env("ROBLOX_API_KEY")
}

resource "roblox_asset" "icon" {
  file            = "./media/icon.png" # square
  display_name    = "Experience icon"
  description     = "Experience icon"
  creator_user_id = 123456789 # the user that owns the API key
}

# The experience's icon and thumbnails are the root place's. Needs asset:read + asset:write.
resource "roblox_place_media" "root" {
  place_id = "1234567890" # the root place

  icon = roblox_asset.icon.id # resolved at apply time; "" removes the icon

  # Image asset ids, in the order shown on the game page. At most 10, no
  # duplicates; alt_text is optional. A reference inside the list isn't
  # supported yet, so these are literal ids (e.g. from `baseplate show`).
  thumbnails = [
    { asset_id = "73372008425730", alt_text = "The main plaza at sunset" },
    { asset_id = "84691374123373" },
  ]
}
