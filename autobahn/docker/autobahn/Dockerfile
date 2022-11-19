FROM python:3.10-alpine3.16

RUN pip install autobahntestsuite

VOLUME /config
VOLUME /report

ENV modeEnv=${mode}
ENV specEnv=${spec}

ENTRYPOINT ["/usr/local/bin/wstest","--mode", "${modeEnv}","--spec","${specEnv}"]
