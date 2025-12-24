FROM golang:1.25.5 AS build
WORKDIR /app

# 1️⃣ Copy only dependency files first
COPY go.mod go.sum ./

# 2️⃣ Download deps (cached unless go.mod/go.sum change)
RUN go mod download

# 3️⃣ Copy the rest of the source
COPY . .

# 4️⃣ Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o upstream .

# ---- Runtime image ----
from scratch as base
copy --from=build /app/upstream /usr/local/bin/
expose 80

cmd [ "upstream" ]
