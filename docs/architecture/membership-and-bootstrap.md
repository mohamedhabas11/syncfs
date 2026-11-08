# Membership and bootstrap

Learner-first admission; one membership change at a time (Inv.8).
Promotion uses joint consensus (`ConfChangeV2` JointImplicit) then an
empty ConfChangeV2 to leave the joint configuration (ADR-0003).

```mermaid
sequenceDiagram
    actor Admin
    participant Learner as new learner
    participant Seed as seed node
    Admin->>Learner: enrollment bundle (one-time nonce)
    Learner->>Seed: prove identity (mTLS)
    Seed-->>Learner: verified snapshot + chunk stream
    Learner->>Learner: scrub + catch up to committed index
    Learner->>Seed: ObserveCatchup(applied >= commit)
    Seed-->>Learner: RequestJointPromotion (ConfChangeV2 JointImplicit)
    Seed-->>Learner: LeaveJoint (empty ConfChangeV2) -> Promoted
    Admin-->>Seed: BeginDrain(old) -> BeginRemove(old)
```

```mermaid
stateDiagram-v2
    [*] --> Learner: BeginAdd
    Learner --> Catchup: ObserveCatchup >= commit
    Catchup --> Joint: RequestJointPromotion
    Joint --> Promoted: LeaveJoint
    Promoted --> Draining: BeginDrain
    Draining --> Removed: BeginRemove (voters > 3)
    Removed --> [*]
```
