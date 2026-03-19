import { publicClient } from "./http/client";
import type { HealthResponse } from "./types";

export const healthCheck = () =>
  publicClient.get<HealthResponse>("/health");
