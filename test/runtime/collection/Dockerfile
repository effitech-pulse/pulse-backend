FROM scratch
COPY api /api
COPY collection-worker /collection-worker
COPY fixture /fixture
COPY parameters.json /parameters.json
USER 65532:65532
ENTRYPOINT ["/api"]
