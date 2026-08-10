create table job_runs (
    id uuid primary key default gen_random_uuid(),
    job text not null,
    started_at timestamptz not null,
    finished_at timestamptz not null,
    status text not null,
    error text,
    created_at timestamptz not null default now()
);

create index job_runs_job_idx on job_runs (job);
create index job_runs_created_at_idx on job_runs (created_at);
