#!/bin/bash
set -e

# Load secrets from files into environment variables
echo "Loading Docker secrets into environment variables..."

# Supabase credentials
if [ -f "/run/secrets/supabase_service_role_key" ]; then
  export SUPABASE_SERVICE_ROLE_KEY=$(cat /run/secrets/supabase_service_role_key | tr -d '"')
  echo "Loaded SUPABASE_SERVICE_ROLE_KEY from secret"
fi

# Redis password
if [ -f "/run/secrets/redis_password" ]; then
  export REDIS_PASSWORD=$(cat /run/secrets/redis_password | tr -d '"')
  echo "Loaded REDIS_PASSWORD from secret"
fi

# Attempt to ping Redis with retries
echo "Testing Redis connection..."
REDIS_HOST=$(echo $REDIS_ADDR | cut -d':' -f1)
REDIS_PORT=$(echo $REDIS_ADDR | cut -d':' -f2)

if command -v nc &> /dev/null; then
  # Retry Redis connection up to 10 times with exponential backoff
  for i in {1..10}; do
    if nc -z $REDIS_HOST $REDIS_PORT -w 2; then
      echo "Redis server is reachable (attempt $i)"
      break
    else
      echo "Redis connection failed (attempt $i/10), retrying in ${i}s..."
      sleep $i
    fi
    
    if [ $i -eq 10 ]; then
      echo "ERROR: Redis server is not reachable after 10 attempts"
      echo "This may cause application startup to fail, but continuing..."
    fi
  done
else
  echo "nc command not available for network testing"
fi

echo "Waiting 3 seconds for final network stabilization..."
sleep 3

echo "Secret loading complete, starting application..."
# Execute the passed command (should be the main application)
exec "$@" 