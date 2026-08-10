create table audit_log (
    id uuid primary key default gen_random_uuid(),
    action text not null,
    subject text not null,
    detail jsonb,
    created_at timestamptz not null default now()
);

create index audit_log_action_idx on audit_log (action);
create index audit_log_created_at_idx on audit_log (created_at);
