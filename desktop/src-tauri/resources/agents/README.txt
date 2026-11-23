Linux agent binaries (imonitord-linux-amd64, -arm64, -arm, -386) are copied here at build time
by `npm run agents` (from ../agent/dist or $IM_AGENTS_DIR) or by CI from the `agents` artifact.
They are uploaded to a node over SSH by the "Install agent via SSH" action.
