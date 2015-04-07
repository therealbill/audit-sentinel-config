

dist-tar: audit-sentinel-config
	@mkdir dist/
	@cp audit-sentinel-config*  dist/


audit-sentinel-config:
	@godep restore
	@go build 

