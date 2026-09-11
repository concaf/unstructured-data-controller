#!/bin/sh
# Update the system CA trust store with any custom certificates mounted
# to /etc/pki/ca-trust/source/anchors/. This makes custom CAs trusted
# across the entire container (Go, curl, OpenSSL, etc.).
if ls /etc/pki/ca-trust/source/anchors/* >/dev/null 2>&1; then
    update-ca-trust extract || true
fi
exec "$@"
