## CMAF Ingest Receiver

This is a very simple CMAF Ingest Receiver meant for testing of the
livesim2 CMAF ingest source.

Start this tool with `go run .` and it will accept incoming
PUT requests on port 8080 and write the content to file structures
in `storage/`.

The first part of the URL path is dropped, thus

`/upload/segtimeline_1/testpic_2s/V300/init.mp4` is stored as
`storage/segtimeline_1/testpic_2s/V300/init.mp4`.
