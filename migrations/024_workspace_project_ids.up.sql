-- XFS project IDs for session workspaces (WORKSPACE_QUOTA=xfs). A sequence
-- keeps them unique across API instances; IDs below 100000 are left to the
-- operator.
CREATE SEQUENCE IF NOT EXISTS workspace_project_id_seq START WITH 100000 MAXVALUE 4294967295;
