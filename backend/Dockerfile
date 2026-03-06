FROM pgvector/pgvector:pg17

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
    postgresql-17-pgtap \
    && rm -rf /var/lib/apt/lists/*
