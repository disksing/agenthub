import { useEffect, useState } from "react";
import { SettingsModal } from "../settings/SettingsModal.jsx";
import { Companion } from "./Companion.jsx";

export function BeeperPage() {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [companionRevision, setCompanionRevision] = useState(0);

  useEffect(() => {
    document.title = "AgentHub Beeper";
  }, []);

  return (
    <main className="beeper-page">
      <Companion
        standalone
        revision={companionRevision}
        onOpenSettings={() => setSettingsOpen(true)}
      />
      {settingsOpen ? (
        <SettingsModal
          onClose={() => setSettingsOpen(false)}
          onSaved={() => setCompanionRevision((current) => current + 1)}
        />
      ) : null}
    </main>
  );
}
