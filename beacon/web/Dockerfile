# beacon web — one file, no dependencies, nothing to install.
FROM node:22-alpine

WORKDIR /app
COPY server.js ./

# ☠️ NON-ROOT ON PURPOSE, THOUGH THE PLATFORM DOES NOT REQUIRE IT.
# Tenant namespaces enforce PodSecurity `restricted` and the renderer makes a
# root image legal by giving the pod its own user namespace (`hostUsers: false`,
# design/46). That works — but a zone whose apiserver has user namespaces off
# DROPS that field on write, and the pod is then rejected at admission for
# having no `runAsNonRoot` either. An image that was already non-root is
# admitted under both profiles, so this line costs nothing and removes a way
# for the demo to fail in a zone-specific manner.
USER node

ENV NODE_ENV=production
EXPOSE 8080
CMD ["node", "server.js"]
