-- One share row per file: creating a share for an already-shared file rotates
-- the access code on the existing row instead of inserting a new one.
-- share_count records how many codes this file has had (create + rotations).

alter table shares add column share_count integer not null default 1;

-- Dedupe before the unique index: keep the newest row per storage_key.
delete from shares a
using shares b
where a.storage_key = b.storage_key
  and (a.created_at < b.created_at
       or (a.created_at = b.created_at and a.id < b.id));

create unique index shares_storage_key_key on shares (storage_key);
