#!/bin/bash

# Generate Root CA
openssl genrsa -out rootCA.key 2048
openssl req -x509 -new -nodes -key rootCA.key -sha256 -days 1024 -out rootCA.crt -subj "/CN=Test Root CA" \
  -addext "basicConstraints = critical, CA:TRUE" \
  -addext "keyUsage = critical, keyCertSign, cRLSign"

# Generate Intermediate Certificate
openssl genrsa -out intermediate.key 2048
openssl req -new -key intermediate.key -out intermediate.csr -subj "/CN=Test Intermediate CA"
openssl x509 -req -in intermediate.csr -CA rootCA.crt -CAkey rootCA.key -CAcreateserial -out intermediate.crt -days 1000 -sha256 \
  -extfile <(echo -e "basicConstraints = critical, CA:TRUE, pathlen:0\nkeyUsage = critical, keyCertSign, cRLSign")

# Generate Leaf Certificate
openssl genrsa -out leaf.key 2048
openssl req -new -key leaf.key -out leaf.csr -subj "/CN=localhost" \
  -addext "subjectAltName = IP:127.0.0.1"
openssl x509 -req -in leaf.csr -CA intermediate.crt -CAkey intermediate.key -CAcreateserial -out leaf.crt -days 500 -sha256 \
  -extfile <(echo -e "authorityKeyIdentifier=keyid,issuer\nbasicConstraints=CA:FALSE\nkeyUsage = digitalSignature, keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=IP:127.0.0.1")

# Generate Leaf Certificate
openssl genrsa -out server.key 2048
openssl req -new -key server.key -out server.csr -subj "/CN=localhost" \
  -addext "subjectAltName = IP:127.0.0.1"
openssl x509 -req -in server.csr -CA rootCA.crt -CAkey rootCA.key -CAcreateserial -out server.crt -days 500 -sha256 \
  -extfile <(echo -e "authorityKeyIdentifier=keyid,issuer\nbasicConstraints=CA:FALSE\nkeyUsage = digitalSignature, keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=IP:127.0.0.1")

rm rootCA.key intermediate.key *.csr *.srl
