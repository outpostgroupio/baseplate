provider "roblox" {
  api_key = env("ROBLOX_API_KEY")
}

resource "roblox_asset" "icon" {
  file            = "./media/icon.png" # square
  display_name    = "Experience icon"
  description     = "Experience icon"
  creator_user_id = 123456789 # the user that owns the API key
}

resource "roblox_asset" "plaza" {
  file            = "./media/plaza.png"
  display_name    = "Plaza thumbnail"
  description     = "Plaza thumbnail"
  creator_user_id = 123456789
}

# The experience's icon and thumbnails are the root place's. Needs asset:read + asset:write.
resource "roblox_place_media" "root" {
  place_id = "1234567890" # the root place

  icon = roblox_asset.icon.id # resolved at apply time; "" removes the icon

  # Image asset ids, in the order shown on the game page. At most 10, no
  # duplicates; alt_text is optional. An id can be a reference or a literal
  # (e.g. an image uploaded elsewhere, from `baseplate show`).
  thumbnails = [
    { asset_id = roblox_asset.plaza.id, alt_text = "The main plaza at sunset" },
    { asset_id = "84691374123373" },
  ]
}
