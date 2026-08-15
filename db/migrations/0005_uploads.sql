-- Every presigned upload is recorded here so the retention worker can find
-- and delete objects the bucket would otherwise keep forever. owner_id is
-- null for anonymous uploads (shorter retention window).

create table uploads (
    storage_key text primary key,
    owner_id uuid,
    file_name text not null,
    size_bytes bigint not null,
    created_at timestamptz not null default now(),
    delete_attempts integer not null default 0,
    last_delete_error text
);

create index uploads_created_at_idx on uploads (created_at);
