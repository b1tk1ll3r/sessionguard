FROM maven:3.9-eclipse-temurin-17 AS build
WORKDIR /src
COPY pom.xml .
COPY src ./src
RUN mvn -B -DskipTests package

FROM scratch
COPY --from=build /src/target/sessionguard-guacamole-0.3.0.jar /sessionguard-guacamole.jar
