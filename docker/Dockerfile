FROM alpine

RUN apk add --no-cache libc6-compat

WORKDIR /app

ADD documize-community-linux-amd64 /app/documize

CMD ["sh", "-c", "/app/documize"]

EXPOSE 5001

# ENV
#
# DOCUMIZEPORT
# DOCUMIZEFORCESSLPORT
# DOCUMIZECERT
# DOCUMIZEKEY
# DOCUMIZESALT (required!)
# DOCUMIZEDB
# DOCUMIZEDBTYPE