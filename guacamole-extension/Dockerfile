FROM maven:3-eclipse-temurin-24 AS build
WORKDIR /src
COPY pom.xml .
COPY src ./src
RUN mvn -B -DskipTests package \
    && JAR="$(find target -maxdepth 1 -type f -name 'sessionguard-guacamole-*.jar' ! -name '*-sources.jar' ! -name '*-javadoc.jar' | head -n 1)" \
    && test -n "$JAR" \
    && cp "$JAR" /tmp/sessionguard-guacamole.jar

FROM scratch
COPY --from=build /tmp/sessionguard-guacamole.jar /sessionguard-guacamole.jar
