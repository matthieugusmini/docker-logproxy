FROM alpine:latest

RUN apk add --no-cache bash

COPY <<EOF /generate_logs.sh
#!/bin/bash

_end() {
  exit 0
}
# end on CTRL-C
trap _end SIGINT
# end on stop
trap _end SIGTERM

while true; do
  # log into stderr
  echo `date` [DEBUG] random log \$RANDOM 1>&2
  # print into stdout
  echo -e "\${RANDOM}\\t\${RANDOM}\\t\${RANDOM}"
  usleep 200000
done
EOF

RUN chmod +x /generate_logs.sh
CMD ["/generate_logs.sh"]
