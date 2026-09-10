#!/bin/sh
# Append any custom CA certificates to the system trust bundle.
# Downstream deployments can mount a ConfigMap with PEM files to
# /etc/pki/ca-trust/source/anchors/ to trust internal CAs at runtime.
if ls /etc/pki/ca-trust/source/anchors/*.pem >/dev/null 2>&1; then
    cat /etc/pki/ca-trust/source/anchors/*.pem >> /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem
fi
exec "$@"
