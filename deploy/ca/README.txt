Optional: root CA (PEM, *.pem/*.crt) of the PKI that issued the OIDC issuer's
certificate, e.g. the internal CA of Caddy Sentinel. Mounted read-only into
the Master (SSL_CERT_DIR). Not needed for public certificates.
