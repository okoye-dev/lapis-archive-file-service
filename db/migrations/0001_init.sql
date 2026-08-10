create table shares (
    id uuid primary key default gen_random_uuid(),
    slug text not null unique,
    owner_id uuid,
    owner_email text,
    recipient_email text,
    storage_key text not null,
    file_name text not null,
    file_size bigint not null,
    code_hash text not null,
    code_salt text not null,
    downloaded_count integer not null default 0,
    created_at timestamptz not null default now(),
    expires_at timestamptz not null
);

create index shares_owner_id_idx on shares (owner_id);
create index shares_expires_at_idx on shares (expires_at);
