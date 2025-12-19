all:
	docker build --pull --no-cache --load -t ericpp/ppwatch .

upload:
	docker push ericpp/ppwatch
