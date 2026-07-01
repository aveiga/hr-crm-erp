-- Per-member section (group) permissions. NULL means full access to every
-- section; a (possibly empty) array restricts the member to those sections.
-- Owners and admins always have full access regardless of this column.
ALTER TABLE memberships ADD COLUMN IF NOT EXISTS allowed_groups TEXT[];
