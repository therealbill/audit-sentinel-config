VERSION=$(shell cat .version)

work-tar: audit-sentinel-config
	@mkdir work/ dist/
	@cp audit-sentinel-config*  work/

audit-sentinel-config:
	@go build 

install-tar: audit-sentinel-config
	@mkdir -p work/usr/share/man/man8 work/usr/sbin/ dist/
	@cp audit-sentinel-config  work/usr/sbin/
	@cp audit-sentinel-config.8 work/usr/share/man/man8/
	@cd work && tar -cvzf ../dist/audit-sentinel-config-${VERSION}.tar.gz usr/* && cd ..

clean:
	@rm audit-sentinel-config
	@rm -rf work/ dist/
