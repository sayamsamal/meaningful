FROM node:22-alpine

# Enable corepack; the pnpm version is pinned via package.json "packageManager"
# (pnpm@10.33.2). pnpm-latest hard-fails `pnpm install` on esbuild's ignored
# build script (ERR_PNPM_IGNORED_BUILDS); 10.33.2 handles it.
RUN corepack enable

WORKDIR /app

# Copy dependency files
COPY package.json pnpm-lock.yaml* ./

# Install dependencies
RUN pnpm install

# Copy the rest of the application
COPY . .

# Expose the port the Nitro/Cloudflare dev server runs on
EXPOSE 3000

# Start the Vite development server and bind to all interfaces
CMD ["pnpm", "run", "dev", "--host"]
