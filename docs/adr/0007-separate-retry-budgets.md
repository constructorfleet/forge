# Gate, review, and CI retries have separate budgets

A gate failure (lint error), a review rejection (architectural concern), and a CI failure (environment issue) are different failure classes. A shared counter would let two lint mistakes plus one reviewer objection exhaust a three-attempt budget despite being entirely ordinary development churn. Every repair — regardless of trigger — must rerun the full quality gate set before proceeding; otherwise Forge gradually becomes a distributed system for proving that yesterday's tests passed.
