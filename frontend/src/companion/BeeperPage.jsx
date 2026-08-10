import { useEffect, useState } from "react";
import { ArrowLeft, SpeakerHigh } from "@phosphor-icons/react";
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
      <header className="beeper-page-header">
        <div className="beeper-page-title">
          <span className="beeper-page-mark"><SpeakerHigh size={20} weight="fill" /></span>
          <div><small>AgentHub</small><h1>Beeper</h1></div>
        </div>
        <a href="/" className="beeper-back-link"><ArrowLeft size={16} />Back to AgentHub</a>
      </header>
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
